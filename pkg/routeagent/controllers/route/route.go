package route

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	v1 "github.com/rancher/submariner/pkg/apis/submariner.io/v1"
	clientset "github.com/rancher/submariner/pkg/client/clientset/versioned"
	informers "github.com/rancher/submariner/pkg/client/informers/externalversions/submariner.io/v1"
	"github.com/vishvananda/netlink"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type Controller struct {
	clusterID       string
	objectNamespace string

	submarinerClientSet clientset.Interface
	clustersSynced      cache.InformerSynced
	endpointsSynced     cache.InformerSynced

	clusterWorkqueue  workqueue.RateLimitingInterface
	endpointWorkqueue workqueue.RateLimitingInterface

	gw      net.IP
	subnets []string

	link *net.Interface
}

func NewController(clusterID string, objectNamespace string, link *net.Interface, submarinerClientSet clientset.Interface,
	clusterInformer informers.ClusterInformer, endpointInformer informers.EndpointInformer) *Controller {
	controller := Controller{
		clusterID:           clusterID,
		objectNamespace:     objectNamespace,
		submarinerClientSet: submarinerClientSet,
		link:                link,
		clustersSynced:      clusterInformer.Informer().HasSynced,
		endpointsSynced:     endpointInformer.Informer().HasSynced,
		clusterWorkqueue:    workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Clusters"),
		endpointWorkqueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Endpoints"),
	}

	clusterInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueCluster,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueCluster(new)
		},
		DeleteFunc: controller.handleRemovedCluster,
	})

	endpointInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueEndpoint,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueEndpoint(new)
		},
		DeleteFunc: controller.handleRemovedEndpoint,
	})

	return &controller
}

func (r *Controller) Run(stopCh <-chan struct{}) error {
	var wg sync.WaitGroup
	wg.Add(1)
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Route Controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for endpoint informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, r.endpointsSynced, r.clustersSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// let's go ahead and pre-populate clusters

	clusters, err := r.submarinerClientSet.SubmarinerV1().Clusters(r.objectNamespace).List(metav1.ListOptions{})

	if err != nil {
		klog.Fatalf("error while retrieving all clusters: %v", err)
	}

	for _, cluster := range clusters.Items {
		if cluster.Spec.ClusterID != r.clusterID {
			r.populateCidrBlockList(append(cluster.Spec.ClusterCIDR, cluster.Spec.ServiceCIDR...))
		}
	}

	klog.Info("Starting workers")
	go wait.Until(r.runClusterWorker, time.Second, stopCh)
	go wait.Until(r.runEndpointWorker, time.Second, stopCh)
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

func (r *Controller) populateCidrBlockList(inputCidrBlocks []string) {
	for _, cidrBlock := range inputCidrBlocks {
		if !containsString(r.subnets, cidrBlock) {
			r.subnets = append(r.subnets, cidrBlock)
		}
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
			return fmt.Errorf("Error while splitting meta namespace key %s: %v", key, err)
		}
		cluster, err := r.submarinerClientSet.SubmarinerV1().Clusters(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("Error retrieving submariner cluster object %s: %v", name, err)
		}

		if cluster.Spec.ClusterID == r.clusterID {
			klog.V(6).Infof("cluster ID matched the cluster ID of this cluster, not adding it to the cidr list")
			return nil
			// no need to reconcile because this endpoint isn't ours
		}

		r.populateCidrBlockList(append(cluster.Spec.ClusterCIDR, cluster.Spec.ServiceCIDR...))

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
			return fmt.Errorf("Error while splitting meta namespace key %s: %v", key, err)
		}
		endpoint, err := r.submarinerClientSet.SubmarinerV1().Endpoints(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("Error retrieving submariner endpoint object %s: %v", name, err)
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

		if endpoint.Spec.Hostname == hostname {
			r.cleanRoutes()
			klog.V(6).Infof("not reconciling routes because we appear to be the gateway host")
			return nil
		}

		klog.V(6).Infof("Setting gateway to gw: %s", endpoint.Spec.PrivateIP.String())

		r.gw = endpoint.Spec.PrivateIP
		r.cleanXfrmPolicies()
		err = r.reconcileRoutes()

		if err != nil {
			r.endpointWorkqueue.AddRateLimited(obj)
			return fmt.Errorf("Error while reconciling routes %v", err)
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
	klog.V(4).Infof("Removed routes from host")
}

func (r *Controller) handleRemovedCluster(obj interface{}) {
	// ideally we should attempt to remove all routes if the endpoint matches our cluster ID
}

func (r *Controller) cleanRoutes() {
	link, err := netlink.LinkByName(r.link.Name)
	if err != nil {
		klog.Errorf("Error retrieving link by name %s: %v", r.link.Name, err)
		return
	}
	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)
	if err != nil {
		klog.Errorf("Error retrieving routes: %v", err)
		return
	}
	for _, route := range currentRouteList {
		klog.V(6).Infof("Processing route %v", route)
		if route.Dst == nil || route.Gw == nil {
			klog.V(6).Infof("Found nil gw or dst")
		} else {
			if containsString(r.subnets, route.Dst.String()) {
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
func (r *Controller) reconcileRoutes() error {
	link, err := netlink.LinkByName(r.link.Name)
	if err != nil {
		return fmt.Errorf("Error retrieving link by name %s: %v", r.link.Name, err)
	}

	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("Error retrieving routes for link %s: %v", r.link.Name, err)
	}

	// First lets delete all of the routes that don't match
	for _, route := range currentRouteList {
		// contains(endpoint destinations, route destination string, and the route gateway is our actual destination
		klog.V(6).Infof("Processing route %v", route)
		if route.Dst == nil || route.Gw == nil {
			klog.V(6).Infof("Found nil gw or dst")
		} else {
			if containsString(r.subnets, route.Dst.String()) && route.Gw.Equal(r.gw) {
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
		return fmt.Errorf("Error retrieving routes for link %s: %v", r.link.Name, err)
	}

	// let's now add the routes that are missing
	for _, cidrBlock := range r.subnets {
		_, dst, err := net.ParseCIDR(cidrBlock)
		if err != nil {
			klog.Errorf("Error parsing cidr block %s: %v", cidrBlock, err)
			break
		}
		route := netlink.Route{
			Dst:       dst,
			Gw:        r.gw,
			LinkIndex: link.Attrs().Index,
		}
		found := false
		for _, curRoute := range currentRouteList {
			if curRoute.Gw == nil || curRoute.Dst == nil {

			} else {
				if curRoute.Gw.Equal(route.Gw) && curRoute.Dst == route.Dst {
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

func containsString(c []string, s string) bool {
	for _, v := range c {
		if v == s {
			return true
		}
	}
	return false
}
