/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package awsvpc provides Route Agent handling for Amazon VPC CNI (EKS).
//
// Enablement matches other CNI handlers: the operator/subctl sets
// SUBMARINER_NETWORKPLUGIN=amazon-vpc-cni, and this handler is registered only
// for that plugin name (see GetNetworkPlugins).
//
// It addresses two dataplane gaps:
//  1. Active-gateway return path: program podIP/32 in the main table via the
//     hosting node's VTEP on vx-submariner so replies from the cable are not sent
//     into the VPC underlay (where Security Groups typically drop foreign pod
//     CIDRs). Node InternalIP/32 routes go into a dedicated PBR table (not main)
//     selected for traffic from the cable interface / remote CIDRs — putting node
//     IPs in main breaks VXLAN underlay (FDB dst = node IP).
//  2. Worker egress with AWS CNI PBR: replicate remote-cluster routes (and the
//     VTEP CIDR) into custom routing tables created for secondary ENIs
//     (see https://github.com/submariner-io/submariner/issues/3697).
//
// Ops fallback: allow the remote cluster's pod/service CIDRs in node Security
// Groups so VPC return can work without VTEP routes (similar to the GKE firewall
// workaround). Prefer these routes for the correct Submariner datapath.
package awsvpc

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/set"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	vxlanIfaceName     = kubeproxy.VxLANIface
	vtepPrefixCIDR     = kubeproxy.VxLANVTepNetworkPrefixCIDR
	reconcilePeriod    = time.Minute
	routeProtocol      = 4 // matches kubeproxy inter-cluster routes
	mainRoutingTableID = 254

	// awsVPCNodeIngressTableID holds node InternalIP/32 via VTEP routes used only
	// for cable ingress / remote-CIDR return traffic (see syncNodeIngressPolicyLocked).
	awsVPCNodeIngressTableID = 152
)

var logger = log.Logger{Logger: logf.Log.WithName("AWSVPC")}

// Handler programs Amazon VPC CNI–specific routes on top of the kubeproxy VXLAN datapath.
type Handler struct {
	event.HandlerBase
	event.NodeHandlerBase

	clientset kubernetes.Interface
	netLink   netlinkAPI.Interface
	podLister corelisters.PodLister

	ipFamily k8snet.IPFamily
	nodeName string
	enabled  bool
	stopCh   chan struct{}
	mutex    sync.Mutex

	// nodeName -> IPv4 InternalIP
	nodeIPs map[string]string
	// pod namespace/name -> last known IPv4 (survives delete tombstones with empty Status)
	podIPs map[string]string
	// pod dstIP/32 -> VTEP (main table; only while on gateway)
	ingressRoutes map[string]string
	// node InternalIP/32 -> VTEP (table awsVPCNodeIngressTableID; only while on gateway)
	nodeIngressRoutes map[string]string
	// cable device name from local endpoint (e.g. "submariner" for AmneziaWG)
	cableIfaceName string
	// per remote endpoint (UID or ns/name) -> IPv4 subnets
	remoteEndpointCIDRs map[string]set.Set[string]
	// union of remoteEndpointCIDRs; used for CNI PBR / ingress policy
	remoteCIDRs set.Set[string]
}

// NewHandler returns an event handler for Amazon VPC CNI. ipFamily is reserved for
// future dual-stack; only IPv4 VPC CNI is handled today.
func NewHandler(clientset kubernetes.Interface, ipFamily k8snet.IPFamily) event.Handler {
	return &Handler{
		clientset:           clientset,
		netLink:             netlinkAPI.New(),
		ipFamily:            ipFamily,
		nodeIPs:             map[string]string{},
		podIPs:              map[string]string{},
		ingressRoutes:       map[string]string{},
		nodeIngressRoutes:   map[string]string{},
		remoteEndpointCIDRs: map[string]set.Set[string]{},
		remoteCIDRs:         set.New[string](),
		stopCh:              make(chan struct{}),
	}
}

func (h *Handler) GetName() string {
	return "AmazonVPC-CNI"
}

func (h *Handler) GetNetworkPlugins() []string {
	return []string{cni.AmazonVPCCNI}
}

func (h *Handler) Init(ctx context.Context) error {
	if h.ipFamily != k8snet.IPv4 {
		logger.Info("Amazon VPC CNI handler supports IPv4 only; skipping")
		return nil
	}

	h.nodeName = os.Getenv("NODE_NAME")
	if h.nodeName == "" {
		var err error

		h.nodeName, err = os.Hostname()
		if err != nil {
			return errors.Wrap(err, "unable to determine node name")
		}
	}

	h.enabled = true
	logger.Infof("Amazon VPC CNI handler enabled on node %q", h.nodeName)

	factory := informers.NewSharedInformerFactory(h.clientset, 0)
	pods := factory.Core().V1().Pods()
	podInformer := pods.Informer()
	h.podLister = pods.Lister()

	_, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			h.onPod(obj.(*corev1.Pod), false)
		},
		UpdateFunc: func(_, newObj any) {
			h.onPod(newObj.(*corev1.Pod), false)
		},
		DeleteFunc: func(obj any) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return
				}

				pod, ok = tombstone.Obj.(*corev1.Pod)
				if !ok {
					return
				}
			}

			h.onPod(pod, true)
		},
	})
	if err != nil {
		return errors.Wrap(err, "error adding pod event handler")
	}

	factory.Start(h.stopCh)

	if !cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced) {
		return errors.New("timed out waiting for pod informer sync")
	}

	go wait.Until(h.reconcile, reconcilePeriod, h.stopCh)

	return nil
}

func (h *Handler) Stop(_ context.Context) error {
	select {
	case <-h.stopCh:
	default:
		close(h.stopCh)
	}

	return nil
}

func (h *Handler) Uninstall(_ context.Context) error {
	if !h.enabled {
		return nil
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.clearIngressRoutesLocked()
	h.clearCNITableRoutesLocked()

	return nil
}

func (h *Handler) LocalEndpointCreated(endpoint *submarinerv1.Endpoint) error {
	return h.syncCableIface(endpoint)
}

func (h *Handler) LocalEndpointUpdated(endpoint *submarinerv1.Endpoint) error {
	return h.syncCableIface(endpoint)
}

func (h *Handler) LocalEndpointRemoved(_ *submarinerv1.Endpoint) error {
	if !h.enabled {
		return nil
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.cableIfaceName = ""
	if h.State().IsOnGateway() {
		h.syncNodeIngressPolicyLocked()
	}

	return nil
}

func (h *Handler) syncCableIface(endpoint *submarinerv1.Endpoint) error {
	if !h.enabled {
		return nil
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.cableIfaceName = endpoint.Spec.BackendConfig[cable.InterfaceNameConfig]
	if h.State().IsOnGateway() {
		h.syncNodeIngressPolicyLocked()
	}

	return nil
}

func (h *Handler) TransitionToGateway() error {
	if !h.enabled {
		return nil
	}

	logger.Info("Became gateway; programming Amazon VPC CNI ingress routes")

	ctx := context.Background()

	if err := h.seedNodeIPs(ctx); err != nil {
		logger.Errorf(err, "Failed to list nodes while becoming gateway")
	}

	h.mutex.Lock()
	h.programAllNodeIngressRoutesLocked()
	h.syncNodeIngressPolicyLocked()
	h.mutex.Unlock()

	if err := h.programAllPodIngressRoutes(); err != nil {
		logger.Errorf(err, "Failed to list pods while becoming gateway")
	}

	h.reconcileIngressRoutes()

	return nil
}

func (h *Handler) TransitionToNonGateway(_ *submarinerv1.Endpoint) error {
	if !h.enabled {
		return nil
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	logger.Info("No longer gateway; clearing Amazon VPC CNI ingress routes")
	h.clearIngressRoutesLocked()

	return nil
}

func (h *Handler) NodeCreated(node *corev1.Node) error {
	return h.syncNode(node, false)
}

func (h *Handler) NodeUpdated(node *corev1.Node) error {
	return h.syncNode(node, false)
}

func (h *Handler) NodeRemoved(node *corev1.Node) error {
	return h.syncNode(node, true)
}

func (h *Handler) RemoteEndpointCreated(endpoint *submarinerv1.Endpoint) error {
	return h.syncRemoteEndpoint(endpoint, false)
}

func (h *Handler) RemoteEndpointUpdated(endpoint *submarinerv1.Endpoint) error {
	return h.syncRemoteEndpoint(endpoint, false)
}

func (h *Handler) RemoteEndpointRemoved(endpoint *submarinerv1.Endpoint) error {
	return h.syncRemoteEndpoint(endpoint, true)
}

func (h *Handler) syncNode(node *corev1.Node, removed bool) error {
	if !h.enabled {
		return nil
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	if removed {
		if ip := h.nodeIPs[node.Name]; ip != "" {
			h.removeNodeIngressRouteLocked(ip + "/32")
		}

		delete(h.nodeIPs, node.Name)

		return nil
	}

	ip := nodeIPv4(node)
	if ip == "" {
		return nil
	}

	prev := h.nodeIPs[node.Name]
	h.nodeIPs[node.Name] = ip

	if !h.State().IsOnGateway() {
		return nil
	}

	if prev != "" && prev != ip {
		h.removeNodeIngressRouteLocked(prev + "/32")
	}

	h.ensureNodeIngressRouteLocked(node.Name, ip)

	return nil
}

func (h *Handler) syncRemoteEndpoint(endpoint *submarinerv1.Endpoint, removed bool) error {
	if !h.enabled {
		return nil
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	key := remoteEndpointKey(endpoint)

	if removed {
		delete(h.remoteEndpointCIDRs, key)
	} else {
		cidrs := set.New[string]()

		for _, subnet := range endpoint.Spec.Subnets {
			if k8snet.IPFamilyOfCIDRString(subnet) == k8snet.IPv4 {
				cidrs.Insert(subnet)
			}
		}

		h.remoteEndpointCIDRs[key] = cidrs
	}

	h.rebuildRemoteCIDRsLocked()
	h.syncCNITableRoutesLocked()

	if h.State().IsOnGateway() {
		h.syncNodeIngressPolicyLocked()
	}

	return nil
}

func (h *Handler) rebuildRemoteCIDRsLocked() {
	h.remoteCIDRs = set.New[string]()

	for _, cidrs := range h.remoteEndpointCIDRs {
		h.remoteCIDRs.Insert(cidrs.UnsortedList()...)
	}
}

func remoteEndpointKey(endpoint *submarinerv1.Endpoint) string {
	if endpoint.UID != "" {
		return string(endpoint.UID)
	}

	return endpoint.Namespace + "/" + endpoint.Name
}

func (h *Handler) reconcile() {
	if !h.enabled {
		return
	}

	// Re-apply from informer/node cache so routes missed while vx-submariner was
	// not ready (TransitionToGateway race) are programmed once the link exists.
	if h.State().IsOnGateway() {
		h.mutex.Lock()
		h.programAllNodeIngressRoutesLocked()
		h.mutex.Unlock()

		if err := h.programAllPodIngressRoutes(); err != nil {
			logger.Errorf(err, "Failed to list pods during reconcile")
		}
	}

	h.reconcileIngressRoutes()

	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.syncCNITableRoutesLocked()

	if h.State().IsOnGateway() {
		h.syncNodeIngressPolicyLocked()
	}
}

func (h *Handler) seedNodeIPs(ctx context.Context) error {
	nodes, err := h.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "list nodes")
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	for i := range nodes.Items {
		if ip := nodeIPv4(&nodes.Items[i]); ip != "" {
			h.nodeIPs[nodes.Items[i].Name] = ip
		}
	}

	return nil
}

func (h *Handler) programAllPodIngressRoutes() error {
	if h.podLister == nil {
		return nil
	}

	pods, err := h.podLister.List(labels.Everything())
	if err != nil {
		return errors.Wrap(err, "list pods from informer cache")
	}

	for _, pod := range pods {
		h.onPod(pod, false)
	}

	return nil
}

func nodeIPv4(node *corev1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP && k8snet.IPFamilyOfString(addr.Address) == k8snet.IPv4 {
			return addr.Address
		}
	}

	return ""
}
