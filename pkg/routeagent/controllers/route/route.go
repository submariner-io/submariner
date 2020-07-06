package route

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable/wireguard"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	informers "github.com/submariner-io/submariner/pkg/client/informers/externalversions/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/util"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	podinformer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type InformerConfigStruct struct {
	SubmarinerClientSet clientset.Interface
	ClientSet           kubernetes.Interface
	EndpointInformer    informers.EndpointInformer
	PodInformer         podinformer.PodInformer
}

type cniInterface struct {
	name      string
	ipAddress string
}

type Controller struct {
	clusterID       string
	objectNamespace string

	submarinerClientSet    clientset.Interface
	clientSet              kubernetes.Interface
	endpointsSynced        cache.InformerSynced
	smRouteAgentPodsSynced cache.InformerSynced

	endpointWorkqueue workqueue.RateLimitingInterface
	podWorkqueue      workqueue.RateLimitingInterface

	localCableDriver string
	localClusterCidr []string
	localServiceCidr []string
	remoteSubnets    *util.StringSet
	routeCacheGWNode *util.StringSet

	gwVxLanMutex *sync.Mutex
	vxlanDevice  *vxLanIface
	remoteVTEPs  *util.StringSet

	isGatewayNode    bool
	defaultHostIface *net.Interface

	cniIface *cniInterface
}

const (
	VxLANIface         = "vx-submariner"
	VxInterfaceWorker  = 0
	VxInterfaceGateway = 1
	VxLANPort          = 4800
	VxLANOverhead      = 50

	// Why VxLANVTepNetworkPrefix is 240?
	// On VxLAN interfaces we need a unique IPAddress which does not collide with the
	// host ip-address. This is going to be tricky as currently there is no specific
	// CIDR in K8s that can be used for this purpose. One option is to take this as an
	// input from the user (i.e., as a configuration parameter), but we want to avoid
	// any additional inputs particularly if there is a way to automate it.

	// So, the approach we are taking is to derive the VxLAN ip from the hostIPAddress
	// as shown below.
	// For example: Say, the host ipaddress is "192.168.1.100/16", we prepend 240 to the
	// host-ip address, derive the vxlan vtepIP (i.e., 240.168.1.100/8) and configure it
	// on the VxLAN interface.

	// The reason behind choosing 240 is that "240.0.0.0/4" is a Reserved IPAddress [*]
	// which normally will not be assigned on any of the hosts. Also, note that the VxLAN
	// IPs are only used within the local cluster and traffic will not leave the cluster
	// with the VxLAN ipaddress.
	// [*] https://en.wikipedia.org/wiki/Reserved_IP_addresses

	VxLANVTepNetworkPrefix = 240
	SmPostRoutingChain     = "SUBMARINER-POSTROUTING"
	SmRouteAgentFilter     = "app=submariner-routeagent"

	// In order to support connectivity from HostNetwork to remoteCluster, route-agent tries
	// to discover the CNIInterface[#] on the respective node and does SNAT of outgoing
	// traffic from that node to the corresponding CNIInterfaceIP. It is to be noted that
	// only traffic destined to the remoteClusters connected via Submariner is SNAT'ed and not
	// any other traffic.
	// At the same time, when Globalnet controller is deployed (i.e., clusters with overlapping
	// Service/Cluster CIDRs) it needs this information so that it can map the CNIInterfaceIP
	// with the corresponding globalIP assigned to the node. Since globalnet controller does
	// not run on all the worker-nodes and there is no well defined mechanism to get the
	// CNIInterfaceIP for each of the nodes, we annotate the node with CNIInterfaceIPInfo as
	// part of route-agent and this will subsequently be used in globalnet controller for
	// supporting connectivity from HostNetwork to remoteClusters.
	// [#] interface on the node that has an IPAddress from the clusterCIDR
	CniInterfaceIp = "submariner.io/cniIfaceIp"

	// To support connectivity for Pods with HostNetworking on the GatewayNode, we program
	// certain routing rules in table 150. As part of these routes, we set the source-ip of
	// the egress traffic to the corresponding CNIInterfaceIP on that host.
	RouteAgentHostNetworkTableID = 150
)

type Operation int

const (
	AddRoute Operation = iota
	DeleteRoute
	FlushRouteTable
)

func newRateLimiter() workqueue.RateLimiter {
	return workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 30*time.Second)
}

func NewController(clusterID string, ClusterCidr []string, ServiceCidr []string, objectNamespace string,
	link *net.Interface, config InformerConfigStruct) *Controller {
	controller := Controller{
		clusterID:              clusterID,
		objectNamespace:        objectNamespace,
		localCableDriver:       "",
		localClusterCidr:       ClusterCidr,
		localServiceCidr:       ServiceCidr,
		submarinerClientSet:    config.SubmarinerClientSet,
		clientSet:              config.ClientSet,
		defaultHostIface:       link,
		isGatewayNode:          false,
		remoteSubnets:          util.NewStringSet(),
		routeCacheGWNode:       util.NewStringSet(),
		remoteVTEPs:            util.NewStringSet(),
		endpointsSynced:        config.EndpointInformer.Informer().HasSynced,
		smRouteAgentPodsSynced: config.PodInformer.Informer().HasSynced,
		gwVxLanMutex:           &sync.Mutex{},
		endpointWorkqueue:      workqueue.NewNamedRateLimitingQueue(newRateLimiter(), "Endpoints"),
		podWorkqueue:           workqueue.NewNamedRateLimitingQueue(newRateLimiter(), "Pods"),
	}

	config.EndpointInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueEndpoint,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueEndpoint(new)
		},
		DeleteFunc: controller.handleRemovedEndpoint,
	})

	config.PodInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueuePod,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueuePod(new)
		},
		DeleteFunc: controller.handleRemovedPod,
	})

	return &controller
}

func (r *Controller) Run(stopCh <-chan struct{}) error {
	var wg sync.WaitGroup
	wg.Add(1)
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Infof("Starting Route Controller. ClusterID: %s, localClusterCIDR: %v, localServiceCIDR: %v", r.clusterID, r.localClusterCidr, r.localServiceCidr)

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for Endpoint informer caches to sync.")
	if ok := cache.WaitForCacheSync(stopCh, r.endpointsSynced, r.smRouteAgentPodsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	cniIface, err := discoverCNIInterface(r.localClusterCidr[0])
	if err == nil {
		// Configure CNI Specific changes
		r.cniIface = cniIface
		err := toggleCNISpecificConfiguration(r.cniIface.name)
		if err != nil {
			return fmt.Errorf("toggleCNISpecificConfiguration returned error. %v", err)
		}
	} else {
		klog.Errorf("discoverCNIInterface returned error %v", err)
	}

	// Create the necessary IPTable chains in the filter and nat tables.
	err = r.createIPTableChains()
	if err != nil {
		return fmt.Errorf("createIPTableChains returned error. %v", err)
	}

	// let's go ahead and pre-populate routes
	endpoints, err := r.submarinerClientSet.SubmarinerV1().Endpoints(r.objectNamespace).List(metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error while retrieving all endpoints: %v", err)
	}

	// Program iptables rules for traffic destined to all the remote cluster CIDRs
	for _, endpoint := range endpoints.Items {
		if endpoint.Spec.ClusterID != r.clusterID {
			if r.overlappingSubnets(endpoint.Spec.Subnets) {
				// Skip processing the endpoint when CIDRs overlap
				continue
			}
			r.updateIptableRulesForInterclusterTraffic(endpoint.Spec.Subnets)
		} else {
			r.localCableDriver = endpoint.Spec.Backend
		}
	}

	if r.cniIface != nil {
		err = r.annotateThisNodeWithCNInterfaceIP()
		if err != nil {
			return err
		}
	} else {
		klog.Warning("cniInterface wasn't found, hostNetwork to remote pod/service connectivity won't work")
	}

	go wait.Until(r.runEndpointWorker, time.Second, stopCh)
	go wait.Until(r.runPodWorker, time.Second, stopCh)
	wg.Wait()
	klog.Info("Route agent workers started")
	<-stopCh
	klog.Info("Route agent stopping")
	return nil
}

func (r *Controller) annotateThisNodeWithCNInterfaceIP() error {
	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("unable to determine hostname: %v", err)
	}

	// Query all the submariner-route-agent daemonSet PODs running in the local cluster.
	podList, err := r.clientSet.CoreV1().Pods(r.objectNamespace).List(metav1.ListOptions{LabelSelector: SmRouteAgentFilter})
	if err != nil {
		return fmt.Errorf("error while retrieving submariner-route-agent pods: %v", err)
	}

	for index, pod := range podList.Items {
		klog.V(log.DEBUG).Infof("In %s, podIP of submariner-route-agent[%d] is %s", r.clusterID, index, pod.Status.PodIP)
		r.populateRemoteVtepIps(pod.Status.PodIP)
		// Each route-agent pod will annotate its own node with the respective CNIInterfaceIP
		if hostname == pod.Spec.NodeName {
			if err = r.annotateNodeWithCNIInterfaceIP(pod.Spec.NodeName); err != nil {
				return fmt.Errorf("error annotating the node %q with cniIfaceIP: %v", hostname, err)
			}
		}
	}
	return nil
}

func (r *Controller) runEndpointWorker() {
	for r.processNextEndpoint() {

	}
}

func (r *Controller) runPodWorker() {
	for r.processNextPod() {

	}
}

func (r *Controller) overlappingSubnets(remoteSubnets []string) bool {
	// If the remoteSubnets [*] overlap with local cluster Pod/Service CIDRs we
	// should not update the IPTable rules on the host, as it will disrupt the
	// functionality of the local cluster. So, lets validate that subnets do not
	// overlap before we program any IPTable rules on the host for inter-cluster
	// traffic.
	// [*] Note: In a non-GlobalNet deployment, remoteSubnets will be a list of
	// Pod/Service CIDRs, whereas in a GlobalNet deployment, it will be a list of
	// globalCIDRs allocated to the clusters.
	for _, serviceCidr := range r.localServiceCidr {
		overlap, err := util.IsOverlappingCIDR(remoteSubnets, serviceCidr)
		if err != nil {
			// Ideally this case will never hit, as the subnets are valid CIDRs
			klog.Warningf("unable to validate overlapping Service CIDR: %s", err)
		}
		if overlap {
			klog.Errorf("Local Service CIDR %q, overlaps with remote cluster %s", serviceCidr, err)
			return true
		}
	}

	for _, podCidr := range r.localClusterCidr {
		overlap, err := util.IsOverlappingCIDR(remoteSubnets, podCidr)
		if err != nil {
			klog.Warningf("unable to validate overlapping Pod CIDR: %s", err)
		}
		if overlap {
			klog.Errorf("Local Pod CIDR %q, overlaps with remote cluster %s", podCidr, err)
			return true
		}
	}
	return false
}

func (r *Controller) updateIptableRulesForInterclusterTraffic(inputCidrBlocks []string) {
	for _, inputCidrBlock := range inputCidrBlocks {
		if !r.remoteSubnets.Contains(inputCidrBlock) {
			r.remoteSubnets.Add(inputCidrBlock)
			err := r.programIptableRulesForInterClusterTraffic(inputCidrBlock)
			if err != nil {
				klog.Errorf("Failed to program iptable rule. %v", err)
			}
		}
	}
}

func (r *Controller) updateRoutingRulesForHostNetworkSupport(inputCidrBlocks []string, operation Operation) {
	if operation == FlushRouteTable {
		r.routeCacheGWNode.DeleteAll()
		// The conversion doesn't introduce a security problem
		// #nosec G204
		cmd := exec.Command("/sbin/ip", "r", "flush", "table", strconv.Itoa(RouteAgentHostNetworkTableID))
		if err := cmd.Run(); err != nil {
			// We can safely ignore this error, as this table will exist only on GW nodes
			klog.V(log.TRACE).Infof("Flushing routing table %d returned error. Can be ignored on non-Gw node: %v",
				RouteAgentHostNetworkTableID, err)
			return
		}
	} else if r.isGatewayNode && r.cniIface != nil {
		// These routing rules are required ONLY on the Gateway Node.
		// On the non-Gateway nodes, we use iptable rules to support this use-case.
		switch operation {
		case AddRoute:
			for _, inputCidrBlock := range inputCidrBlocks {
				if r.routeCacheGWNode.Add(inputCidrBlock) {
					if err := r.configureRoute(inputCidrBlock, operation); err != nil {
						r.routeCacheGWNode.Delete(inputCidrBlock)
						klog.Errorf("Failed to add route %q for HostNetwork support on the Gateway node: %v",
							inputCidrBlock, err)
					}
				}
			}
		case DeleteRoute:
			for _, inputCidrBlock := range inputCidrBlocks {
				if r.routeCacheGWNode.Delete(inputCidrBlock) {
					if err := r.configureRoute(inputCidrBlock, operation); err != nil {
						klog.Errorf("Failed to delete route %q for HostNetwork support on the Gateway node. %v",
							inputCidrBlock, err)
					}
				}
			}
		}
	}
}

func (r *Controller) populateRemoteVtepIps(vtepIP string) {
	if !r.remoteVTEPs.Contains(vtepIP) {
		r.remoteVTEPs.Add(vtepIP)
	}
}

func (r *Controller) getVxlanVtepIPAddress(ipAddr string) (net.IP, error) {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		return nil, fmt.Errorf("invalid ipAddr [%s]", ipAddr)
	}

	ipSlice[0] = strconv.Itoa(VxLANVTepNetworkPrefix)
	vxlanIP := net.ParseIP(strings.Join(ipSlice, "."))
	return vxlanIP, nil
}

func (r *Controller) getHostIfaceIPAddress() (net.IP, error) {
	addrs, err := r.defaultHostIface.Addrs()
	if err != nil {
		return nil, err
	}

	if len(addrs) > 0 {
		for i := range addrs {
			ipAddr, _, err := net.ParseCIDR(addrs[i].String())
			if err != nil {
				klog.Errorf("Unable to ParseCIDR : %v\n", addrs)
			}
			if ipAddr.To4() != nil {
				return ipAddr, nil
			}
		}
	}
	return nil, nil
}

func (r *Controller) createVxLANInterface(ifaceType int, gatewayNodeIP net.IP) error {
	ipAddr, err := r.getHostIfaceIPAddress()
	if err != nil {
		return fmt.Errorf("unable to retrieve the IPv4 address on the Host %v", err)
	}

	vtepIP, err := r.getVxlanVtepIPAddress(ipAddr.String())
	if err != nil {
		return fmt.Errorf("failed to derive the vxlan vtepIP for %s, %v", ipAddr, err)
	}

	// Derive the MTU based on the default outgoing interface
	vxlanMtu := r.defaultHostIface.MTU - VxLANOverhead

	if ifaceType == VxInterfaceGateway {
		attrs := &vxLanAttributes{
			name:     VxLANIface,
			vxlanId:  100,
			group:    nil,
			srcAddr:  nil,
			vtepPort: VxLANPort,
			mtu:      vxlanMtu,
		}

		r.vxlanDevice, err = newVxlanIface(attrs)
		if err != nil {
			return fmt.Errorf("failed to create vxlan interface on Gateway Node: %v", err)
		}

		for _, fdbAddress := range r.remoteVTEPs.Elements() {
			err = r.vxlanDevice.AddFDB(net.ParseIP(fdbAddress), "00:00:00:00:00:00")
			if err != nil {
				return fmt.Errorf("failed to add FDB entry on the Gateway Node vxlan iface %v", err)
			}
		}

		// Enable loose mode (rp_filter=2) reverse path filtering on the vxlan interface.
		// We won't ever create rp_filter, and its permissions are 644
		// #nosec G306
		err = ioutil.WriteFile("/proc/sys/net/ipv4/conf/"+VxLANIface+"/rp_filter", []byte("2"), 0644)
		if err != nil {
			return fmt.Errorf("unable to update vxlan rp_filter proc entry, err: %s", err)
		} else {
			klog.V(log.DEBUG).Infof("Successfully configured rp_filter to loose mode(2) on %s", VxLANIface)
		}
	} else if ifaceType == VxInterfaceWorker {
		// non-Gateway/Worker Node
		attrs := &vxLanAttributes{
			name:     VxLANIface,
			vxlanId:  100,
			group:    gatewayNodeIP,
			srcAddr:  nil,
			vtepPort: VxLANPort,
			mtu:      vxlanMtu,
		}

		r.vxlanDevice, err = newVxlanIface(attrs)
		if err != nil {
			return fmt.Errorf("failed to create vxlan interface on non-Gateway Node: %v", err)
		}
	}

	err = r.vxlanDevice.configureIPAddress(vtepIP, net.CIDRMask(8, 32))
	if err != nil {
		return fmt.Errorf("failed to configure vxlan interface ipaddress on the Gateway Node %v", err)
	}

	return nil
}

func (r *Controller) processNextPod() bool {
	obj, shutdown := r.podWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer r.podWorkqueue.Done(obj)

		key := obj.(string)
		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			r.podWorkqueue.Forget(obj)
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}

		pod, err := r.clientSet.CoreV1().Pods(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				r.podWorkqueue.Forget(obj)
				klog.Infof("submariner-route-agent pod for key %q not found - probably was deleted", key)
				return nil
			}

			r.podWorkqueue.AddRateLimited(obj)
			return fmt.Errorf("error retrieving submariner-route-agent pod object %s: %v", name, err)
		}

		klog.V(log.DEBUG).Infof("Processing submariner-route-agent pod %q with host IP %q, pod IP %q",
			name, pod.Status.HostIP, pod.Status.PodIP)

		r.populateRemoteVtepIps(pod.Status.PodIP)

		r.gwVxLanMutex.Lock()
		defer r.gwVxLanMutex.Unlock()

		// A new Node (identified via a Submariner-route-agent daemonset pod event) is added to the cluster.
		// On the GatewayDevice, update the vxlan fdb entry (i.e., remote Vtep) for the newly added node.
		if r.isGatewayNode {
			if r.vxlanDevice != nil {
				err := r.vxlanDevice.AddFDB(net.ParseIP(pod.Status.PodIP), "00:00:00:00:00:00")
				if err != nil {
					r.podWorkqueue.Forget(obj)
					return fmt.Errorf("failed to add FDB entry on the Gateway Node vxlan iface %v", err)
				}
				klog.Infof("FDB entry added on the Gateway node's vxlan iface for "+
					"route-agent pod %q, IP %q", pod.Name, pod.Status.PodIP)
			} else {
				r.podWorkqueue.AddRateLimited(obj)
				klog.Errorf("vxlanDevice is not yet created on the Gateway node")
				return nil
			}
		}

		r.podWorkqueue.Forget(obj)
		klog.V(log.DEBUG).Infof("Successfully processed submariner-route-agent pod %q", name)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (r *Controller) processNextEndpoint() bool {
	obj, shutdown := r.endpointWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer r.endpointWorkqueue.Done(obj)
		key := obj.(string)

		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}

		endpoint, err := r.submarinerClientSet.SubmarinerV1().Endpoints(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				r.endpointWorkqueue.Forget(obj)
				klog.Infof("Endpoint for key %q not found - probably was deleted", key)
				return nil
			}

			r.endpointWorkqueue.AddRateLimited(obj)
			return fmt.Errorf("error retrieving submariner endpoint object %s: %v", name, err)
		}

		klog.V(log.TRACE).Infof("Processing endpoint %q : %s", name, endpoint)

		if endpoint.Spec.ClusterID != r.clusterID {
			// Before we make any changes on the host, verify that subnets do not overlap.
			if r.overlappingSubnets(endpoint.Spec.Subnets) {
				// Skip processing the endpoint when CIDRs are overlapping.
				r.endpointWorkqueue.Forget(obj)
				return nil
			}

			klog.Infof("Updating iptable rules for remote cluster's Endpoint %q with subnets %v",
				endpoint.Name, endpoint.Spec.Subnets)
			r.updateIptableRulesForInterclusterTraffic(endpoint.Spec.Subnets)

			r.gwVxLanMutex.Lock()
			// Add routes to the new endpoint on the GatewayNode.
			r.updateRoutingRulesForHostNetworkSupport(endpoint.Spec.Subnets, AddRoute)
			r.gwVxLanMutex.Unlock()

			r.endpointWorkqueue.Forget(obj)
			return nil
		}

		klog.Infof("Processing local Endpoint %s with IP %s, Host %s for this cluster",
			endpoint.Name, endpoint.Spec.PrivateIP, endpoint.Spec.Hostname)

		hostname, err := os.Hostname()
		if err != nil {
			klog.Fatalf("unable to determine hostname: %v", err)
		}

		// If the endpoint hostname matches with our hostname, it implies we are on gateway node
		if endpoint.Spec.Hostname == hostname {
			r.cleanVxSubmarinerRoutes()
			klog.Infof("This route agent is running on the active gateway node")

			r.gwVxLanMutex.Lock()
			defer r.gwVxLanMutex.Unlock()

			r.isGatewayNode = true
			klog.Infof("Creating the vxlan interface: %s on the gateway node", VxLANIface)
			err = r.createVxLANInterface(VxInterfaceGateway, nil)
			if err != nil {
				klog.Fatalf("Unable to create VxLAN interface on gateway node (%s): %v", hostname, err)
			}

			err = r.configureIPRule(AddRoute)
			if err != nil {
				klog.Errorf("Unable to add ip rule to table %d on Gateway node %s: %v",
					RouteAgentHostNetworkTableID, hostname, err)
			}

			if r.localCableDriver == "" {
				r.localCableDriver = endpoint.Spec.Backend
			}
			// On the GatewayNode, add routes for the remoteSubnets
			r.updateRoutingRulesForHostNetworkSupport(r.remoteSubnets.Elements(), AddRoute)

			r.endpointWorkqueue.Forget(obj)
			return nil
		}

		klog.Infof("This route agent is running on a non-gateway/non-active node")

		localClusterGwNodeIP := net.ParseIP(endpoint.Spec.PrivateIP)
		remoteVtepIP, err := r.getVxlanVtepIPAddress(localClusterGwNodeIP.String())
		if err != nil {
			r.endpointWorkqueue.Forget(obj)
			return fmt.Errorf("failed to derive the remoteVtepIP %v", err)
		}

		r.gwVxLanMutex.Lock()
		r.isGatewayNode = false
		klog.Infof("Creating the vxlan interface %s with gateway node IP %s", VxLANIface, localClusterGwNodeIP)
		err = r.createVxLANInterface(VxInterfaceWorker, localClusterGwNodeIP)
		if err != nil {
			klog.Fatalf("Unable to create VxLAN interface on non-GatewayNode (%s): %v", endpoint.Spec.Hostname, err)
		}
		// If the active Gateway transitions to a new node, we flush the HostNetwork routing table.
		r.updateRoutingRulesForHostNetworkSupport(nil, FlushRouteTable)
		r.gwVxLanMutex.Unlock()

		// NOTE(mangelajo): This may not belong here, it's a gateway cleanup thing
		r.cleanStrongswanRoutingTable()
		r.cleanXfrmPolicies()

		err = r.reconcileRoutes(remoteVtepIP)
		if err != nil {
			r.endpointWorkqueue.AddRateLimited(obj)
			return fmt.Errorf("error while reconciling routes %v", err)
		}

		err = r.configureIPRule(DeleteRoute)
		if err != nil {
			klog.Errorf("Unable to delete ip rule to table %d on non-Gateway node %s: %v",
				RouteAgentHostNetworkTableID, hostname, err)
		}

		r.endpointWorkqueue.Forget(obj)
		klog.V(log.DEBUG).Infof("Successfully processed local Endpoint %q", endpoint.Name)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (r *Controller) enqueueEndpoint(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(log.DEBUG).Infof("Enqueueing endpoint for route controller %v", obj)
	r.endpointWorkqueue.AddRateLimited(key)
}

func (r *Controller) enqueuePod(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}

	pod := obj.(*k8sv1.Pod)
	// Add the POD event to the workqueue only if the sm-route-agent podIP does not exist in the local cache.
	if pod.Status.HostIP != "" && !r.remoteVTEPs.Contains(pod.Status.HostIP) {
		klog.V(log.DEBUG).Infof("Enqueueing sm-route-agent-pod event, ip: %s", pod.Status.HostIP)
		r.podWorkqueue.AddRateLimited(key)
	}
}

func (r *Controller) handleRemovedEndpoint(obj interface{}) {
	// ideally we should attempt to remove all routes if the endpoint matches our cluster ID
	var object *v1.Endpoint
	var ok bool
	klog.V(log.TRACE).Infof("Handling object in handleRemoveEndpoint")
	if object, ok = obj.(*v1.Endpoint); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Could not convert object %v to an Endpoint", obj)
			return
		}
		object, ok = tombstone.Obj.(*v1.Endpoint)
		if !ok {
			klog.Errorf("Could not convert object tombstone %v to an Endpoint", tombstone.Obj)
			return
		}
	}
	klog.V(log.DEBUG).Infof("Informed of removed endpoint: %v", object.String())

	r.gwVxLanMutex.Lock()
	defer r.gwVxLanMutex.Unlock()

	if object.Spec.ClusterID != r.clusterID {
		// TODO: Handle a remote endpoint removal use-case
		//         - remove routes to remote cluster
		//         - remove related iptable rules
		r.updateRoutingRulesForHostNetworkSupport(object.Spec.Subnets, DeleteRoute)
		return
	}

	if r.vxlanDevice == nil {
		klog.Warningf("vxlanDevice is not set - ignoring removed local Endpoint %q", object.Name)
		return
	}

	if r.isGatewayNode {
		klog.Infof("Local Gateway Endpoint %q with IP %s was removed: deleting vxlan interface",
			object.Name, object.Spec.PrivateIP)
		r.cleanVxSubmarinerRoutes()
		// Active Gateway seems to have transitioned to a new node, flush the Host Network routing table.
		r.updateRoutingRulesForHostNetworkSupport(nil, FlushRouteTable)
		err := r.configureIPRule(DeleteRoute)
		if err != nil {
			klog.Errorf("Unable to delete ip rule to table %d : %v", RouteAgentHostNetworkTableID, err)
		}
	} else {
		klog.Infof("Local non-gateway Endpoint %q with IP %q was removed: deleting vxlan interface",
			object.Name, object.Spec.PrivateIP)
	}

	r.isGatewayNode = false
	err := r.vxlanDevice.deleteVxLanIface()
	r.vxlanDevice = nil
	if err != nil {
		klog.Errorf("Failed to delete the the vxlan interface on Endpoint removal: %v", err)
	}
}

func (r *Controller) handleRemovedPod(obj interface{}) {
	klog.V(log.DEBUG).Infof("Removing podIP in route controller %v", obj)
	pod := obj.(*k8sv1.Pod)

	if r.remoteVTEPs.Delete(pod.Status.HostIP) {
		r.gwVxLanMutex.Lock()
		defer r.gwVxLanMutex.Unlock()

		if r.isGatewayNode && r.vxlanDevice != nil {
			ret := r.vxlanDevice.DelFDB(net.ParseIP(pod.Status.PodIP), "00:00:00:00:00:00")
			if ret != nil {
				klog.Errorf("Failed to delete FDB entry on the Gateway Node vxlan iface %v", ret)
			}
		}
	}
}

func (r *Controller) cleanVxSubmarinerRoutes() {
	link, err := netlink.LinkByName(VxLANIface)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			klog.Errorf("Error retrieving link by name %q: %v", VxLANIface, err)
			return
		}
	}
	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)
	if err != nil {
		klog.Errorf("Unable to cleanup routes, error retrieving routes on the link %s: %v", VxLANIface, err)
		return
	}
	for i := range currentRouteList {
		klog.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])
		if currentRouteList[i].Dst == nil || currentRouteList[i].Gw == nil {
			klog.V(log.DEBUG).Infof("Found nil gw or dst")
		} else {
			if r.remoteSubnets.Contains(currentRouteList[i].Dst.String()) {
				klog.V(log.DEBUG).Infof("Removing route %s", currentRouteList[i])
				if err = netlink.RouteDel(&currentRouteList[i]); err != nil {
					klog.Errorf("Error removing route %s: %v", currentRouteList[i], err)
				}
			}
		}
	}
}

//NOTE: the following two methods method will probably need to be either moved to another
//      process, or re-architected in some form. Those are strongswan/ipsec specific
//      methods, and eventually we will have other types of cable engines. At least
//      we may want to call the cable-engine specific cleanups depending on the cable
//      engine which was used.

func (r *Controller) cleanStrongswanRoutingTable() {
	cmd := exec.Command("/sbin/ip", "r", "flush", "table", "220")
	if err := cmd.Run(); err != nil {
		// We can safely ignore this error, as this table
		// won't exist in most nodes (only gateway nodes)
		return
	}
}

func (r *Controller) cleanXfrmPolicies() {
	currentXfrmPolicyList, err := netlink.XfrmPolicyList(syscall.AF_INET)

	if err != nil {
		klog.Errorf("Error retrieving current xfrm policies: %v", err)
		return
	}

	if len(currentXfrmPolicyList) > 0 {
		klog.Infof("Cleaning up %d XFRM policies", len(currentXfrmPolicyList))
	}
	for i := range currentXfrmPolicyList {
		klog.V(log.DEBUG).Infof("Deleting XFRM policy %s", currentXfrmPolicyList[i])
		if err = netlink.XfrmPolicyDel(&currentXfrmPolicyList[i]); err != nil {
			klog.Errorf("Error Deleting XFRM policy %s: %v", currentXfrmPolicyList[i], err)
		}
	}
}

// Reconcile the routes installed on this device using rtnetlink
func (r *Controller) reconcileRoutes(vxlanGw net.IP) error {
	klog.V(log.DEBUG).Infof("Reconciling routes to gw: %s", vxlanGw.String())

	link, err := netlink.LinkByName(VxLANIface)
	if err != nil {
		return fmt.Errorf("error retrieving link by name %s: %v", VxLANIface, err)
	}

	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("error retrieving routes for link %s: %v", VxLANIface, err)
	}

	// First lets delete all of the routes that don't match
	for i := range currentRouteList {
		// contains(endpoint destinations, route destination string, and the route gateway is our actual destination
		klog.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])
		if currentRouteList[i].Dst == nil || currentRouteList[i].Gw == nil {
			klog.V(log.DEBUG).Infof("Found nil gw or dst")
		} else {
			if r.remoteSubnets.Contains(currentRouteList[i].Dst.String()) && currentRouteList[i].Gw.Equal(vxlanGw) {
				klog.V(log.DEBUG).Infof("Found route %s with gw %s already installed", currentRouteList[i], currentRouteList[i].Gw)
			} else {
				klog.V(log.DEBUG).Infof("Removing route %s", currentRouteList[i])
				if err = netlink.RouteDel(&currentRouteList[i]); err != nil {
					klog.Errorf("Error removing route %s: %v", currentRouteList[i], err)
				}
			}
		}
	}

	currentRouteList, err = netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("error retrieving routes for link %s: %v", VxLANIface, err)
	}

	// let's now add the routes that are missing
	for _, cidrBlock := range r.remoteSubnets.Elements() {
		_, dst, err := net.ParseCIDR(cidrBlock)
		if err != nil {
			klog.Errorf("Error parsing cidr block %s: %v", cidrBlock, err)
			break
		}
		route := netlink.Route{
			Dst:       dst,
			Gw:        vxlanGw,
			Scope:     unix.RT_SCOPE_UNIVERSE,
			LinkIndex: link.Attrs().Index,
			Protocol:  4,
		}
		found := false
		for _, curRoute := range currentRouteList {
			if curRoute.Gw == nil || curRoute.Dst == nil {

			} else {
				if curRoute.Gw.Equal(route.Gw) && curRoute.Dst.String() == route.Dst.String() {
					klog.V(log.DEBUG).Infof("Found equivalent route, not adding")
					found = true
				}
			}
		}

		if !found {
			err = netlink.RouteAdd(&route)
			if err != nil {
				klog.Errorf("Error adding route %s: %v", route.String(), err)
			}
		}
	}
	return nil
}

func (r *Controller) configureIPRule(operation Operation) error {
	if r.cniIface != nil {
		rule := netlink.NewRule()
		rule.Table = RouteAgentHostNetworkTableID
		rule.Priority = RouteAgentHostNetworkTableID

		switch operation {
		case AddRoute:
			err := netlink.RuleAdd(rule)
			if err != nil && !os.IsExist(err) {
				return fmt.Errorf("failed to add ip rule %s: %v", rule.String(), err)
			}
		case DeleteRoute:
			err := netlink.RuleDel(rule)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to delete ip rule %s: %v", rule.String(), err)
			}
		}
	}
	return nil
}

func (r *Controller) configureRoute(remoteSubnet string, operation Operation) error {
	src := net.ParseIP(r.cniIface.ipAddress)
	_, dst, err := net.ParseCIDR(remoteSubnet)
	if err != nil {
		return fmt.Errorf("error parsing cidr block %s: %v", remoteSubnet, err)
	}

	ifaceIndex := r.defaultHostIface.Index
	// TODO: Add support for this in the CableDrivers themselves.
	if r.localCableDriver == "wireguard" {
		if wg, err := net.InterfaceByName(wireguard.DefaultDeviceName); err == nil {
			ifaceIndex = wg.Index
		} else {
			klog.Errorf("Wireguard interface %s not found on the node.", wireguard.DefaultDeviceName)
		}
	}
	route := netlink.Route{
		Dst:       dst,
		Src:       src,
		Scope:     unix.RT_SCOPE_LINK,
		LinkIndex: ifaceIndex,
		Protocol:  4,
		Table:     RouteAgentHostNetworkTableID,
	}

	switch operation {
	case AddRoute:
		err = netlink.RouteAdd(&route)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("error adding the route %s: %v", route.String(), err)
		}
	case DeleteRoute:
		err = netlink.RouteDel(&route)
		if err != nil {
			return fmt.Errorf("error deleting the route %s: %v", route.String(), err)
		}
	}
	return nil
}
