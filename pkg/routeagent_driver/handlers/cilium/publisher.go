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

// Package cilium provides a Route Agent handler for clusters using the Cilium CNI.
//
// The ClusterMesh publisher embeds etcd and publishes remote Submariner CIDRs as
// Cilium ClusterMesh IPIdentityPair keys (CIDR→HostIP) so Host:BPF agents program
// ipcache tunnelendpoint. It activates when SUBMARINER_NETWORKPLUGIN=cilium.
// Incompatible with real Cilium ClusterMesh unless carefully isolated (alpha).
// Cert paths/URLs via SUBMARINER_CILIUM_CM_* env. Operator/subctl should distribute
// matching TLS material to route-agent and Secret cilium-clustermesh.
//
// See https://github.com/submariner-io/submariner/issues/3168.
package cilium

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("Cilium")}

const (
	defaultCMRemoteName = "submariner"
	// defaultCMClusterID is reserved for the synthetic Submariner peer; local
	// Cilium cluster-id must not use this value (see operator/subctl checks).
	defaultCMClusterID = uint32(255)
	defaultCMClientURL = "http://127.0.0.1:12379"
	defaultCMPeerURL   = "http://127.0.0.1:12380"

	// reconcileTimeout bounds event-driven reconciles so a stuck apiserver
	// cannot block Stop/shutdown indefinitely.
	reconcileTimeout = 30 * time.Second
)

// PublisherConfig configures the ClusterMesh-compatible CIDR publisher.
type PublisherConfig struct {
	// EtcdClient, when set, backs the store without starting embedded etcd (tests).
	EtcdClient EtcdClient
	// RemoteName is the synthetic Cilium ClusterMesh peer name (Secret key prefix).
	RemoteName         string
	ListenClientURL    string
	AdvertiseClientURL string
	ListenPeerURL      string
	AdvertisePeerURL   string
	DataDir            string
	CertFile           string
	KeyFile            string
	CAFile             string
	LocalNodeName      string
	LocalNodeIP        string
	// PreferredHostIP overrides automatic HostIP selection when set and ≠ LocalNodeIP.
	// On the Submariner gateway, SelectHostIP prefers cilium_host when no override is set.
	PreferredHostIP string

	// ClusterID is the synthetic remote cluster-id advertised in cluster-config.
	ClusterID uint32
}

// PublisherEnv holds optional env overrides (prefix submariner via Process).
// Activation is via SUBMARINER_NETWORKPLUGIN=cilium (same as other CNI handlers).
type PublisherEnv struct {
	CiliumCMRemoteName string `default:"submariner"             envconfig:"CILIUM_CM_REMOTE_NAME"`
	CiliumCMListenURL  string `default:"http://127.0.0.1:12379" envconfig:"CILIUM_CM_LISTEN_URL"`
	CiliumCMPeerURL    string `default:"http://127.0.0.1:12380" envconfig:"CILIUM_CM_PEER_URL"`
	CiliumCMDataDir    string `envconfig:"CILIUM_CM_DATA_DIR"`
	CiliumCMCertFile   string `envconfig:"CILIUM_CM_CERT_FILE"`
	CiliumCMKeyFile    string `envconfig:"CILIUM_CM_KEY_FILE"`
	CiliumCMCAFile     string `envconfig:"CILIUM_CM_CA_FILE"`
	CiliumCMHostIP     string `envconfig:"CILIUM_CM_HOST_IP"`
	CiliumCMClusterID  uint32 `default:"255"                    envconfig:"CILIUM_CM_CLUSTER_ID"`
}

type clusterMeshPublisher struct {
	event.HandlerBase
	event.NodeHandlerBase

	client kubernetes.Interface
	cfg    PublisherConfig

	store        *etcdStore
	gatewayIP    string
	published    map[string]string // cidr -> hostIP
	started      bool
	bootstrapped bool

	heartbeatCancel context.CancelFunc
	heartbeatDone   chan struct{}

	stopOnce sync.Once
}

// NewClusterMeshPublisher returns a handler that publishes remote Submariner
// CIDRs into an embedded etcd using Cilium ClusterMesh key formats, so
// cilium-agent programs ipcache tunnelendpoint entries (Host:BPF path).
//
// The registry only runs it when SUBMARINER_NETWORKPLUGIN=cilium.
func NewClusterMeshPublisher(client kubernetes.Interface, cfg *PublisherConfig) event.Handler {
	applyPublisherDefaults(cfg)

	return &clusterMeshPublisher{
		client:    client,
		cfg:       *cfg,
		published: map[string]string{},
	}
}

func applyPublisherDefaults(cfg *PublisherConfig) {
	if cfg.RemoteName == "" {
		cfg.RemoteName = defaultCMRemoteName
	}

	if cfg.ClusterID == 0 {
		cfg.ClusterID = defaultCMClusterID
	}

	if cfg.ListenClientURL == "" {
		cfg.ListenClientURL = defaultCMClientURL
	}

	if cfg.AdvertiseClientURL == "" {
		cfg.AdvertiseClientURL = cfg.ListenClientURL
	}

	if cfg.ListenPeerURL == "" {
		cfg.ListenPeerURL = defaultCMPeerURL
	}

	if cfg.AdvertisePeerURL == "" {
		cfg.AdvertisePeerURL = cfg.ListenPeerURL
	}
}

func (h *clusterMeshPublisher) GetNetworkPlugins() []string {
	return []string{cni.Cilium}
}

func (h *clusterMeshPublisher) GetName() string {
	return "Cilium ClusterMesh publisher"
}

func (h *clusterMeshPublisher) Init(ctx context.Context) error {
	if h.cfg.LocalNodeIP == "" {
		return errors.New("ClusterMesh publisher requires LocalNodeIP")
	}

	if h.cfg.EtcdClient != nil {
		h.store = newEtcdStoreWithClient(h.cfg.EtcdClient)
	} else {
		store, err := startEtcdStore(ctx, &EtcdStoreConfig{
			DataDir:            h.cfg.DataDir,
			ListenClientURL:    h.cfg.ListenClientURL,
			AdvertiseClientURL: h.cfg.AdvertiseClientURL,
			ListenPeerURL:      h.cfg.ListenPeerURL,
			AdvertisePeerURL:   h.cfg.AdvertisePeerURL,
			CertFile:           h.cfg.CertFile,
			KeyFile:            h.cfg.KeyFile,
			CAFile:             h.cfg.CAFile,
		})
		if err != nil {
			return errors.Wrap(err, "start ClusterMesh publisher etcd")
		}

		h.store = store
	}

	h.started = true

	logger.Infof("Cilium ClusterMesh publisher started (node=%q remote=%q cluster-id=%d listen=%s localIP=%s)",
		h.cfg.LocalNodeName, h.cfg.RemoteName, h.cfg.ClusterID, h.cfg.ListenClientURL, h.cfg.LocalNodeIP)

	h.startHeartbeat(ctx)

	// Registry only calls Stop for handlers that successfully Init. If reconcile
	// fails here, close the store ourselves to avoid leaking etcd/goroutines/ports.
	if err := h.reconcile(ctx); err != nil {
		h.stopHeartbeat()

		if stopErr := h.store.Close(); stopErr != nil {
			logger.Errorf(stopErr, "error closing store after Init failure")
		}

		return err
	}

	return nil
}

func (h *clusterMeshPublisher) Stop(ctx context.Context) error {
	// Delete keys while etcd is still up so watching cilium-agents observe removals
	// before the peer disappears. uninstall() calls StopHandlers before Uninstall.
	var err error

	h.stopOnce.Do(func() {
		err = h.shutdown(ctx)
	})

	return err
}

func (h *clusterMeshPublisher) Uninstall(_ context.Context) error {
	// Keys are cleared in Stop (see uninstall() order in route-agent main).
	logger.Info("Cilium ClusterMesh publisher uninstall: published keys were cleared on Stop")
	return nil
}

func (h *clusterMeshPublisher) shutdown(ctx context.Context) error {
	if h.store == nil {
		return nil
	}

	h.stopHeartbeat()

	h.started = false

	for publishedCIDR := range h.published {
		if err := h.store.DeleteRoute(ctx, publishedCIDR); err != nil {
			logger.Warningf("Failed to delete Cilium CM route %s on shutdown: %v", publishedCIDR, err)
		}
	}

	h.published = map[string]string{}

	if err := h.store.DeleteClusterConfig(ctx, h.cfg.RemoteName); err != nil {
		logger.Warningf("Failed to delete Cilium CM cluster-config %q on shutdown: %v",
			h.cfg.RemoteName, err)
	}

	return errors.Wrap(h.store.Close(), "error closing ClusterMesh publisher store")
}

func (h *clusterMeshPublisher) startHeartbeat(ctx context.Context) {
	ctx, h.heartbeatCancel = context.WithCancel(ctx)
	h.heartbeatDone = make(chan struct{})

	go func() {
		defer close(h.heartbeatDone)

		wait.Until(func() {
			if err := h.store.TouchHeartbeat(ctx); err != nil && ctx.Err() == nil {
				logger.Warningf("Failed to update heartbeat: %v", err)
			}
		}, cmHeartbeatInterval, ctx.Done())
	}()
}

func (h *clusterMeshPublisher) stopHeartbeat() {
	if h.heartbeatCancel == nil {
		return
	}

	h.heartbeatCancel()
	<-h.heartbeatDone
}

func (h *clusterMeshPublisher) reconcileOnEvent() error {
	ctx, cancel := context.WithTimeout(context.Background(), reconcileTimeout)
	defer cancel()

	return h.reconcile(ctx)
}

func (h *clusterMeshPublisher) RemoteEndpointCreated(_ *submV1.Endpoint) error {
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) RemoteEndpointUpdated(_ *submV1.Endpoint) error {
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) RemoteEndpointRemoved(_ *submV1.Endpoint) error {
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) StaleRemoteEndpointRemoved(_ *submV1.Endpoint) error {
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	h.setGatewayIP(endpoint)
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	h.setGatewayIP(endpoint)
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) TransitionToGateway() error {
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) TransitionToNonGateway(localEndpoint *submV1.Endpoint) error {
	h.setGatewayIP(localEndpoint)
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) NodeCreated(_ *corev1.Node) error {
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) NodeUpdated(_ *corev1.Node) error {
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) NodeRemoved(_ *corev1.Node) error {
	return h.reconcileOnEvent()
}

func (h *clusterMeshPublisher) setGatewayIP(endpoint *submV1.Endpoint) {
	if endpoint == nil {
		return
	}

	if ip := endpoint.Spec.GetPrivateIP(k8snet.IPv4); ip != "" {
		h.gatewayIP = ip
	}
}

func (h *clusterMeshPublisher) reconcile(ctx context.Context) error {
	if !h.started || h.store == nil {
		return nil
	}

	hostIP, err := h.resolveHostIP(ctx)
	if err != nil {
		logger.Warningf("Cilium ClusterMesh publisher: cannot select HostIP on node %q: %v",
			h.cfg.LocalNodeName, err)

		return nil
	}

	if !h.bootstrapped {
		if err := h.store.Bootstrap(ctx, h.cfg.RemoteName, h.cfg.ClusterID); err != nil {
			return errors.Wrap(err, "bootstrap ClusterMesh publisher")
		}

		h.bootstrapped = true
	}

	desired := h.desiredRemoteCIDRs()
	desiredSet := make(map[string]struct{}, len(desired))

	for _, c := range desired {
		desiredSet[c] = struct{}{}

		if prev, ok := h.published[c]; ok && prev == hostIP {
			continue
		}

		if err := h.store.UpsertRoute(ctx, c, hostIP, h.cfg.ClusterID); err != nil {
			return errors.Wrapf(err, "upsert route %s -> %s", c, hostIP)
		}

		h.published[c] = hostIP
		logger.Infof("Published Cilium CM route %s HostIP=%s (node=%q)", c, hostIP, h.cfg.LocalNodeName)
	}

	for c := range h.published {
		if _, ok := desiredSet[c]; ok {
			continue
		}

		if err := h.store.DeleteRoute(ctx, c); err != nil {
			return errors.Wrapf(err, "delete route %s", c)
		}

		delete(h.published, c)
		logger.Infof("Deleted Cilium CM route %s (node=%q)", c, h.cfg.LocalNodeName)
	}

	return nil
}

func (h *clusterMeshPublisher) desiredRemoteCIDRs() []string {
	desired := make([]string, 0, len(h.State().GetRemoteEndpoints()))

	for i := range h.State().GetRemoteEndpoints() {
		ep := &h.State().GetRemoteEndpoints()[i]
		desired = append(desired, cidr.ExtractSubnets(k8snet.IPv4, ep.Spec.Subnets)...)
	}

	slices.Sort(desired)

	return slices.Compact(desired)
}

func (h *clusterMeshPublisher) resolveHostIP(ctx context.Context) (string, error) {
	nodeIPs, err := h.listHostIPCandidateIPs(ctx)
	if err != nil {
		return "", err
	}

	overlayIP, _ := CiliumHostIPv4(nil)

	return SelectHostIP(h.cfg.LocalNodeIP, h.gatewayIP, h.cfg.PreferredHostIP, nodeIPs, overlayIP)
}

func (h *clusterMeshPublisher) listHostIPCandidateIPs(ctx context.Context) ([]string, error) {
	if h.client == nil {
		return nil, nil
	}

	nodes, err := h.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "list nodes for HostIP selection")
	}

	return hostIPCandidateIPs(nodes.Items), nil
}
