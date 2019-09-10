package route

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	informers "github.com/submariner-io/submariner/pkg/client/informers/externalversions/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/util"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	k8sv1 "k8s.io/api/core/v1"
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
	ClusterInformer     informers.ClusterInformer
	EndpointInformer    informers.EndpointInformer
	PodInformer         podinformer.PodInformer
}

type Controller struct {
	clusterID       string
	objectNamespace string

	submarinerClientSet    clientset.Interface
	clientSet              kubernetes.Interface
	clustersSynced         cache.InformerSynced
	endpointsSynced        cache.InformerSynced
	smRouteAgentPodsSynced cache.InformerSynced

	clusterWorkqueue  workqueue.RateLimitingInterface
	endpointWorkqueue workqueue.RateLimitingInterface
	podWorkqueue      workqueue.RateLimitingInterface

	localClusterCidr []string
	localServiceCidr []string
	remoteSubnets    *util.StringSet

	vxlanDevice *vxLanIface
	remoteVTEPs *util.StringSet

	isGatewayNode    bool
	defaultHostIface *net.Interface
}

const (
	VxLANIface         = "vx-submariner"
	VxInterfaceWorker  = 0
	VxInterfaceGateway = 1
	VxLANPort          = 4800

	/* Why VxLANVTepNetworkPrefix is 240?
	   On VxLAN interfaces we need a unique IPAddress which does not collide with the
	   host ip-address. This is going to be tricky as currently there is no specific
	   CIDR in K8s that can be used for this purpose. If really required, this can be
	   taken as an input from the user (i.e., as a configuration parameter).
	   But we want to avoid any additional inputs particularly if there is a way to automate it.

	   So, the approach we are taking is to derive the VxLAN ip from the hostIPAddress as shown below.
	   Example: Say the host ipaddress is "192.168.1.100/16", we prepend 240 to the host-ip address
	   and configure it on the VxLAN interface (i.e., 240.168.1.100/16).

	   The reason behind choosing 240 is that "240.0.0.0/4" is a Reserved IPAddress [*] which
	   normally will not be assigned on any of the hosts. Also, note that the VxLAN IPs that are
	   so configured are only used within the local cluster and traffic will not leave the cluster
	   with the VxLAN ipaddress.

	   [*] https://en.wikipedia.org/wiki/Reserved_IP_addresses */

	VxLANVTepNetworkPrefix = 240
	SmPostRoutingChain     = "SUBMARINER-POSTROUTING"
	SmRouteAgentFilter     = "app=submariner-routeagent"
)

func NewController(clusterID string, ClusterCidr []string, ServiceCidr []string, objectNamespace string,
	link *net.Interface, config InformerConfigStruct) *Controller {
	controller := Controller{
		clusterID:              clusterID,
		objectNamespace:        objectNamespace,
		localClusterCidr:       ClusterCidr,
		localServiceCidr:       ServiceCidr,
		submarinerClientSet:    config.SubmarinerClientSet,
		clientSet:              config.ClientSet,
		defaultHostIface:       link,
		isGatewayNode:          false,
		remoteSubnets:          util.NewStringSet(),
		remoteVTEPs:            util.NewStringSet(),
		clustersSynced:         config.ClusterInformer.Informer().HasSynced,
		endpointsSynced:        config.EndpointInformer.Informer().HasSynced,
		smRouteAgentPodsSynced: config.PodInformer.Informer().HasSynced,
		clusterWorkqueue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Clusters"),
		endpointWorkqueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Endpoints"),
		podWorkqueue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Pods"),
	}

	config.ClusterInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueCluster,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueCluster(new)
		},
		DeleteFunc: controller.handleRemovedCluster,
	})

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
	klog.Info("Waiting for endpoint informer caches to sync.")
	if ok := cache.WaitForCacheSync(stopCh, r.endpointsSynced, r.clustersSynced, r.smRouteAgentPodsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// Create the necessary IPTable chains in the filter and nat tables.
	err := r.createIPTableChains()
	if err != nil {
		return fmt.Errorf("createIPTableChains returned error. %v", err)
	}

	// let's go ahead and pre-populate clusters
	clusters, err := r.submarinerClientSet.SubmarinerV1().Clusters(r.objectNamespace).List(metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error while retrieving all clusters: %v", err)
	}

	// Program iptables rules for traffic destined to all the remote cluster CIDRs
	for _, cluster := range clusters.Items {
		if cluster.Spec.ClusterID != r.clusterID {
			r.updateIptableRulesForInterclusterTraffic(append(cluster.Spec.ClusterCIDR, cluster.Spec.ServiceCIDR...))
		}
	}

	// Query all the submariner-route-agent daemonSet PODs running in the local cluster.
	podList, err := r.clientSet.CoreV1().Pods(r.objectNamespace).List(metav1.ListOptions{LabelSelector: SmRouteAgentFilter})
	if err != nil {
		return fmt.Errorf("error while retrieving submariner-route-agent pods: %v", err)
	}

	for index, pod := range podList.Items {
		klog.V(4).Infof("In %s, podIP of submariner-route-agent[%d] is %s", r.clusterID, index, pod.Status.PodIP)
		r.populateRemoteVtepIps(pod.Status.PodIP)
	}

	klog.Info("Starting workers")
	go wait.Until(r.runClusterWorker, time.Second, stopCh)
	go wait.Until(r.runEndpointWorker, time.Second, stopCh)
	go wait.Until(r.runPodWorker, time.Second, stopCh)
	wg.Wait()
	<-stopCh
	klog.Info("Shutting down workers")
	return nil
}

func (r *Controller) runClusterWorker() {
	for r.processNextCluster() {

	}
}

func (r *Controller) runEndpointWorker() {
	for r.processNextEndpoint() {

	}
}

func (r *Controller) runPodWorker() {
	for r.processNextPod() {

	}
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

func (r *Controller) populateRemoteVtepIps(vtepIP string) {
	if !r.remoteVTEPs.Contains(vtepIP) {
		r.remoteVTEPs.Add(vtepIP)
	}
}

func (r *Controller) processNextCluster() bool {
	obj, shutdown := r.clusterWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer r.clusterWorkqueue.Done(obj)
		klog.V(4).Infof("Processing cluster object: %v", obj)
		key := obj.(string)
		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}
		cluster, err := r.submarinerClientSet.SubmarinerV1().Clusters(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("error retrieving submariner cluster object %s: %v", name, err)
		}

		if cluster.Spec.ClusterID == r.clusterID {
			klog.V(6).Infof("cluster ID matched the cluster ID of this cluster, not adding it to the cidr list")
			return nil
			// no need to reconcile because this endpoint isn't ours
		}

		r.updateIptableRulesForInterclusterTraffic(append(cluster.Spec.ClusterCIDR, cluster.Spec.ServiceCIDR...))

		r.clusterWorkqueue.Forget(obj)
		klog.V(4).Infof("cluster processed by route controller")
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
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

func (r *Controller) getHostIfaceIPAddress() (net.IP, *net.IPNet, error) {
	addrs, err := r.defaultHostIface.Addrs()
	if err != nil {
		return nil, nil, err
	}

	if len(addrs) > 0 {
		for i := range addrs {
			ipAddr, ipNetwork, err := net.ParseCIDR(addrs[i].String())
			if err != nil {
				klog.Errorf("Unable to ParseCIDR : %v\n", addrs)
			}
			if ipAddr.To4() != nil {
				return ipAddr, ipNetwork, nil
			}
		}
	}
	return nil, nil, nil
}

func (r *Controller) createVxLANInterface(ifaceType int, gatewayNodeIP net.IP) error {
	ipAddr, vtepMask, err := r.getHostIfaceIPAddress()
	if err != nil {
		return fmt.Errorf("unable to retrieve the IPv4 address on the Host %v", err)
	}

	vtepIP, err := r.getVxlanVtepIPAddress(ipAddr.String())
	if err != nil {
		return fmt.Errorf("failed to derive the vxlan vtepIP for %s, %v", ipAddr, err)
	}

	if ifaceType == VxInterfaceGateway {
		attrs := &vxLanAttributes{
			name:     VxLANIface,
			vxlanId:  100,
			group:    nil,
			srcAddr:  nil,
			vtepPort: VxLANPort,
			mtu:      1450,
		}

		r.vxlanDevice, err = newVxlanIface(attrs)
		if err != nil {
			return fmt.Errorf("failed to create vxlan interface on Gateway Node: %v", err)
		}

		for fdbAddress := range r.remoteVTEPs.Set {
			err = r.vxlanDevice.AddFDB(net.ParseIP(fdbAddress), "00:00:00:00:00:00")
			if err != nil {
				return fmt.Errorf("failed to add FDB entry on the Gateway Node vxlan iface %v", err)
			}
		}

		// Enable loose mode (rp_filter=2) reverse path filtering on the vxlan interface.
		err = ioutil.WriteFile("/proc/sys/net/ipv4/conf/"+VxLANIface+"/rp_filter", []byte("2"), 0644)
		if err != nil {
			return fmt.Errorf("unable to update vxlan rp_filter proc entry, err: %s", err)
		} else {
			klog.Info("Successfully configured rp_filter to loose mode(2) ")
		}

	} else if ifaceType == VxInterfaceWorker {
		// non-Gateway/Worker Node
		attrs := &vxLanAttributes{
			name:     VxLANIface,
			vxlanId:  100,
			group:    gatewayNodeIP,
			srcAddr:  vtepIP,
			vtepPort: VxLANPort,
			mtu:      1450,
		}

		r.vxlanDevice, err = newVxlanIface(attrs)
		if err != nil {
			return fmt.Errorf("failed to create vxlan interface on non-Gateway Node: %v", err)
		}
	}

	err = r.vxlanDevice.configureIPAddress(vtepIP, vtepMask.Mask)
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
			r.podWorkqueue.Forget(obj)
			return fmt.Errorf("error retrieving submariner-route-agent pod object %s: %v", name, err)
		}

		klog.V(4).Infof("In processNextPod, POD HostIP is %s", pod.Status.HostIP)
		r.populateRemoteVtepIps(pod.Status.PodIP)

		// A new Node (identified via a Submariner-route-agent daemonset pod event) is added to the cluster.
		// On the GatewayDevice, update the vxlan fdb entry (i.e., remote Vtep) for the newly added node.
		if r.isGatewayNode {
			if r.vxlanDevice != nil {
				err := r.vxlanDevice.AddFDB(net.ParseIP(pod.Status.PodIP), "00:00:00:00:00:00")
				if err != nil {
					r.podWorkqueue.Forget(obj)
					return fmt.Errorf("failed to add FDB entry on the Gateway Node vxlan iface %v", err)
				}
			} else {
				r.podWorkqueue.AddRateLimited(obj)
				klog.Errorf("vxlanDevice is not yet created on the Gateway node")
				return nil
			}
		}

		r.podWorkqueue.Forget(obj)
		klog.V(4).Infof("Pod event processed by route controller")
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
		klog.V(4).Infof("Handling object in handleEndpoint")
		klog.V(4).Infof("Processing endpoint object: %v", obj)
		key := obj.(string)
		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}
		endpoint, err := r.submarinerClientSet.SubmarinerV1().Endpoints(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("error retrieving submariner endpoint object %s: %v", name, err)
		}

		if endpoint.Spec.ClusterID != r.clusterID {
			klog.V(6).Infof("Endpoint didn't match the cluster ID of this cluster")
			return nil
			// no need to reconcile because this endpoint isn't ours
		}

		hostname, err := os.Hostname()
		if err != nil {
			klog.Fatalf("unable to determine hostname: %v", err)
		}

		klog.V(6).Infof("Local Cluster Gateway Node IP is %s", endpoint.Spec.PrivateIP)

		// If the endpoint hostname matches with our hostname, it implies we are on gateway node
		if endpoint.Spec.Hostname == hostname {
			r.cleanRoutes()
			r.isGatewayNode = true
			if r.createVxLANInterface(VxInterfaceGateway, nil) != nil {
				klog.Fatalf("Unable to create VxLAN interface on GatewayNode (%s): %v", hostname, err)
			}
			klog.V(6).Infof("not reconciling routes because we appear to be the gateway host")
			return nil
		}

		localClusterGwNodeIP := net.ParseIP(endpoint.Spec.PrivateIP)
		remoteVtepIP, err := r.getVxlanVtepIPAddress(localClusterGwNodeIP.String())
		if err != nil {
			return fmt.Errorf("failed to derive the remoteVtepIP %v", err)
		}

		r.isGatewayNode = false
		if r.createVxLANInterface(VxInterfaceWorker, localClusterGwNodeIP) != nil {
			klog.Fatalf("Unable to create VxLAN interface on non-GatewayNode (%s): %v", endpoint.Spec.Hostname, err)
		}

		r.cleanXfrmPolicies()
		err = r.reconcileRoutes(remoteVtepIP)
		if err != nil {
			r.endpointWorkqueue.AddRateLimited(obj)
			return fmt.Errorf("error while reconciling routes %v", err)
		}

		r.endpointWorkqueue.Forget(obj)
		klog.V(4).Infof("endpoint processed by route controller")
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (r *Controller) enqueueCluster(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(4).Infof("Enqueueing cluster for route controller %v", obj)
	r.clusterWorkqueue.AddRateLimited(key)
}

func (r *Controller) enqueueEndpoint(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(4).Infof("Enqueueing endpoint for route controller %v", obj)
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
	if !r.remoteVTEPs.Contains(pod.Status.HostIP) {
		klog.V(4).Infof("Enqueueing sm-route-agent-pod event, ip: %s", pod.Status.HostIP)
		r.podWorkqueue.AddRateLimited(key)
	}
}

func (r *Controller) handleRemovedEndpoint(obj interface{}) {
	// ideally we should attempt to remove all routes if the endpoint matches our cluster ID
	var object *v1.Endpoint
	var ok bool
	klog.V(4).Infof("Handling object in handleEndpoint")
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
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.V(4).Infof("Informed of removed endpoint for route controller object: %v", object)
	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Could not retrieve hostname: %v", err)
	}
	if object.Spec.Hostname == hostname {
		r.cleanRoutes()
	}

	if object.Spec.ClusterID == r.clusterID {
		klog.V(6).Infof("Endpoint matches the cluster ID of this cluster")
		err := r.vxlanDevice.deleteVxLanIface()
		if err != nil {
			klog.Errorf("Failed to delete the the vxlan interface on endpoint removal: %v", err)
			return
		}
		r.vxlanDevice = nil
	}

	klog.V(4).Infof("Removed routes from host")
}

func (r *Controller) handleRemovedCluster(obj interface{}) {
	// ideally we should attempt to remove all routes if the endpoint matches our cluster ID
}

func (r *Controller) handleRemovedPod(obj interface{}) {
	klog.V(6).Infof("Removing podIP in route controller %v", obj)
	pod := obj.(*k8sv1.Pod)

	if r.remoteVTEPs.Contains(pod.Status.HostIP) {
		r.remoteVTEPs.Delete(pod.Status.HostIP)
		if r.isGatewayNode && r.vxlanDevice != nil {
			ret := r.vxlanDevice.DelFDB(net.ParseIP(pod.Status.PodIP), "00:00:00:00:00:00")
			if ret != nil {
				klog.Errorf("Failed to delete FDB entry on the Gateway Node vxlan iface %v", ret)
			}
		}
	}
}

func (r *Controller) cleanRoutes() {
	link, err := netlink.LinkByName(VxLANIface)
	if err != nil {
		klog.Errorf("Error retrieving link by name %s: %v", VxLANIface, err)
		return
	}
	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)
	if err != nil {
		klog.Errorf("Error retrieving routes on the link %s: %v", VxLANIface, err)
		return
	}
	for _, route := range currentRouteList {
		klog.V(6).Infof("Processing route %v", route)
		if route.Dst == nil || route.Gw == nil {
			klog.V(6).Infof("Found nil gw or dst")
		} else {
			if r.remoteSubnets.Contains(route.Dst.String()) {
				klog.V(6).Infof("Removing route %s", route.String())
				if err = netlink.RouteDel(&route); err != nil {
					klog.Errorf("Error removing route %s: %v", route.String(), err)
				}
			}
		}
	}
}

func (r *Controller) cleanXfrmPolicies() {

	currentXfrmPolicyList, err := netlink.XfrmPolicyList(syscall.AF_INET)

	if err != nil {
		klog.Errorf("Error retrieving current xfrm policies: %v", err)
		return
	}

	for _, xfrmPolicy := range currentXfrmPolicyList {
		klog.V(6).Infof("Deleting XFRM policy %s", xfrmPolicy.String())
		if err = netlink.XfrmPolicyDel(&xfrmPolicy); err != nil {
			klog.Errorf("Error Deleting XFRM policy %s: %v", xfrmPolicy.String(), err)
		}
	}
}

// Reconcile the routes installed on this device using rtnetlink
func (r *Controller) reconcileRoutes(vxlanGw net.IP) error {
	link, err := netlink.LinkByName(VxLANIface)
	if err != nil {
		return fmt.Errorf("error retrieving link by name %s: %v", VxLANIface, err)
	}

	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("error retrieving routes for link %s: %v", VxLANIface, err)
	}

	// First lets delete all of the routes that don't match
	for _, route := range currentRouteList {
		// contains(endpoint destinations, route destination string, and the route gateway is our actual destination
		klog.V(6).Infof("Processing route %v", route)
		if route.Dst == nil || route.Gw == nil {
			klog.V(6).Infof("Found nil gw or dst")
		} else {
			if r.remoteSubnets.Contains(route.Dst.String()) && route.Gw.Equal(vxlanGw) {
				klog.V(6).Infof("Found route %s with gw %s already installed", route.String(), route.Gw.String())
			} else {
				klog.V(6).Infof("Removing route %s", route.String())
				if err = netlink.RouteDel(&route); err != nil {
					klog.Errorf("Error removing route %s: %v", route.String(), err)
				}
			}
		}
	}

	currentRouteList, err = netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("error retrieving routes for link %s: %v", VxLANIface, err)
	}

	// let's now add the routes that are missing
	for cidrBlock := range r.remoteSubnets.Set {
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
					klog.V(6).Infof("Found equivalent route, not adding")
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
