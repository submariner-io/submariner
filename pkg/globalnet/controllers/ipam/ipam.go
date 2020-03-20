package ipam

import (
	"fmt"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/submariner-io/submariner/pkg/log"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

func NewController(spec *SubmarinerIpamControllerSpecification, config *InformerConfigStruct, globalCIDR string) (*Controller, error) {
	exclusionMap := make(map[string]bool)
	for _, v := range spec.ExcludeNS {
		exclusionMap[v] = true
	}
	pool, err := NewIpPool(globalCIDR)
	if err != nil {
		return nil, err
	}

	iptableHandler, err := iptables.New()
	if err != nil {
		return nil, err
	}

	ipamController := &Controller{
		kubeClientSet:    config.KubeClientSet,
		serviceWorkqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Services"),
		servicesSynced:   config.ServiceInformer.Informer().HasSynced,
		podWorkqueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Pods"),
		podsSynced:       config.PodInformer.Informer().HasSynced,

		excludeNamespaces: exclusionMap,
		pool:              pool,
		ipt:               iptableHandler,
	}
	klog.Info("Setting up event handlers")
	config.ServiceInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ipamController.enqueueObject(obj, ipamController.serviceWorkqueue)
		},
		UpdateFunc: func(old, newObj interface{}) {
			ipamController.handleUpdateService(old, newObj)
		},
		DeleteFunc: ipamController.handleRemovedService,
	}, handlerResync)
	config.PodInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ipamController.enqueueObject(obj, ipamController.podWorkqueue)
		},
		UpdateFunc: func(old, newObj interface{}) {
			ipamController.handleUpdatePod(old, newObj)
		},
		DeleteFunc: ipamController.handleRemovedPod,
	}, handlerResync)

	return ipamController, nil
}

func (i *Controller) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting IPAM Controller")

	err := i.initIPTableChains()
	if err != nil {
		return fmt.Errorf("initIPTableChains returned error. %v", err)
	}

	// Currently submariner global-net implementation works only with kube-proxy.
	if chainExists, _ := i.doesIPTablesChainExist("nat", kubeProxyServiceChainName); !chainExists {
		return fmt.Errorf("%q chain missing, cluster does not seem to use kube-proxy", kubeProxyServiceChainName)
	}

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, i.servicesSynced, i.podsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	go wait.Until(i.runServiceWorker, time.Second, stopCh)
	go wait.Until(i.runPodWorker, time.Second, stopCh)
	<-stopCh
	klog.Info("Shutting down workers")
	i.cleanupIPTableRules()
	return nil
}

func (i *Controller) runServiceWorker() {
	for i.processNextObject(i.serviceWorkqueue, i.serviceGetter, i.serviceUpdater) {
	}
}

func (i *Controller) runPodWorker() {
	for i.processNextObject(i.podWorkqueue, i.podGetter, i.podUpdater) {
	}
}

func (i *Controller) processNextObject(objWorkqueue workqueue.RateLimitingInterface, objGetter func(namespace, name string) (runtime.Object, error), objUpdater func(runtimeObj runtime.Object, key string) error) bool {
	obj, shutdown := objWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer objWorkqueue.Done(obj)

		key := obj.(string)
		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			objWorkqueue.Forget(obj)
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Retrieve the latest version of object before attempting update
			// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
			runtimeObj, err := objGetter(ns, name)

			if err != nil {
				if errors.IsNotFound(err) {
					// already deleted Forget and return
					objWorkqueue.Forget(obj)
				} else {
					// could be API error, so we want to requeue
					logAndRequeue(key, objWorkqueue)
				}
				return fmt.Errorf("error retrieving submariner-ipam-controller object %s: %v", name, err)
			}

			switch runtimeObj.(type) {
			case *k8sv1.Service:
				service := runtimeObj.(*k8sv1.Service)
				switch i.evaluateService(service) {
				case Ignore:
					objWorkqueue.Forget(obj)
					return nil
				case Requeue:
					objWorkqueue.AddRateLimited(obj)
					return fmt.Errorf("service %s requeued %d times", key, objWorkqueue.NumRequeues(obj))
				}
			case *k8sv1.Pod:
				pod := runtimeObj.(*k8sv1.Pod)
				// Process pod event only when it has an ipaddress. Pods skipped here will be handled subsequently
				// during podUpdate event when an ipaddress is assigned to it.
				if pod.Status.PodIP == "" {
					objWorkqueue.Forget(obj)
					return nil
				}

				// Privileged pods that use hostNetwork will be ignored.
				if pod.Status.PodIP == pod.Status.HostIP {
					klog.V(log.DEBUG).Infof("Ignoring pod %s on host %s as it uses hostNetworking", pod.Name, pod.Status.PodIP)
					return nil
				}
			}

			return objUpdater(runtimeObj, key)
		})
		if retryErr != nil {
			return retryErr
		}

		objWorkqueue.Forget(obj)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (i *Controller) enqueueObject(obj interface{}, workqueue workqueue.RateLimitingInterface) {
	if key := i.getEnqueueKey(obj); key != "" {
		klog.V(log.TRACE).Infof("Enqueueing %v for ipam controller", key)
		workqueue.AddRateLimited(key)
	}
}

func (i *Controller) getEnqueueKey(obj interface{}) string {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return ""
	}
	ns, _, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(err)
		return ""
	}
	if i.excludeNamespaces[ns] {
		return ""
	}
	return key
}

func (i *Controller) handleUpdateService(old interface{}, newObj interface{}) {
	//TODO: further minimize duplication between this and handleUpdatePod
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(newObj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	oldGlobalIp := old.(*k8sv1.Service).GetAnnotations()[submarinerIpamGlobalIp]
	newGlobalIp := newObj.(*k8sv1.Service).GetAnnotations()[submarinerIpamGlobalIp]
	if oldGlobalIp != newGlobalIp && newGlobalIp != i.pool.GetAllocatedIp(key) {
		klog.V(log.DEBUG).Infof("GlobalIp changed from %s to %s for %s", oldGlobalIp, newGlobalIp, key)
		i.enqueueObject(newObj, i.serviceWorkqueue)
	}
}

func (i *Controller) handleUpdatePod(old interface{}, newObj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(newObj); err != nil {
		utilruntime.HandleError(err)
		return
	}

	oldPodIp := old.(*k8sv1.Pod).Status.PodIP
	updatedPodIp := newObj.(*k8sv1.Pod).Status.PodIP
	podHostIP := newObj.(*k8sv1.Pod).Status.HostIP
	// Pod events that are skipped during addEvent are handled here when they are assigned an ipaddress.
	if updatedPodIp != "" && oldPodIp != updatedPodIp {
		klog.V(log.DEBUG).Infof("In handleUpdatePod, pod %s is now assigned %s address, enqueing", old.(*k8sv1.Pod).Name, updatedPodIp)
		i.enqueueObject(newObj, i.podWorkqueue)
		return
	}

	// When the POD is getting terminated, sometimes we get pod update event with podIp removed.
	if oldPodIp != "" && updatedPodIp == "" {
		klog.V(log.DEBUG).Infof("Pod %s with ip %s is being terminated", old.(*k8sv1.Pod).Name, oldPodIp)
		i.handleRemovedPod(old)
	}

	// Ignore privileged pods that use hostNetwork
	if updatedPodIp != "" && updatedPodIp == podHostIP {
		klog.V(log.DEBUG).Infof("Pod %s on host %s uses hostNetwork, ignoring", old.(*k8sv1.Pod).Name, podHostIP)
		return
	}

	oldGlobalIp := old.(*k8sv1.Pod).GetAnnotations()[submarinerIpamGlobalIp]
	newGlobalIp := newObj.(*k8sv1.Pod).GetAnnotations()[submarinerIpamGlobalIp]
	if oldGlobalIp != newGlobalIp && newGlobalIp != i.pool.GetAllocatedIp(key) {
		klog.V(log.DEBUG).Infof("GlobalIp changed from %s to %s for %s", oldGlobalIp, newGlobalIp, key)
		i.enqueueObject(newObj, i.podWorkqueue)
	}
}

func (i *Controller) handleRemovedService(obj interface{}) {
	//TODO: further minimize duplication between this and handleRemovedPod
	var service *k8sv1.Service
	var ok bool
	var key string
	var err error
	if service, ok = obj.(*k8sv1.Service); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Could not convert object %v to Service", obj)
			return
		}
		service, ok = tombstone.Obj.(*k8sv1.Service)
		if !ok {
			klog.Errorf("Could not convert object tombstone %v to Service", tombstone.Obj)
			return
		}
	}
	if !i.excludeNamespaces[service.Namespace] {
		globalIp := service.Annotations[submarinerIpamGlobalIp]
		if globalIp != "" {
			if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
				utilruntime.HandleError(err)
				return
			}
			i.pool.Release(key)
			i.syncServiceRules(service, globalIp, DeleteRules)
			klog.V(log.DEBUG).Infof("Released ip %s for service %s", globalIp, key)
		}
	}
}

func (i *Controller) handleRemovedPod(obj interface{}) {
	var pod *k8sv1.Pod
	var ok bool
	var key string
	var err error
	if pod, ok = obj.(*k8sv1.Pod); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Could not convert object %v to pod", obj)
			return
		}
		pod, ok = tombstone.Obj.(*k8sv1.Pod)
		if !ok {
			klog.Errorf("Could not convert object tombstone %v to Pod", tombstone.Obj)
			return
		}
	}
	if !i.excludeNamespaces[pod.Namespace] {
		globalIp := pod.Annotations[submarinerIpamGlobalIp]
		if globalIp != "" && pod.Status.PodIP != "" {
			if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
				utilruntime.HandleError(err)
				return
			}
			i.pool.Release(key)
			i.syncPodRules(pod.Status.PodIP, globalIp, DeleteRules)
			klog.V(log.DEBUG).Infof("Released GlobalIp %s for pod %s", globalIp, key)
		}
	}
}

func (i *Controller) annotateGlobalIp(key string, globalIp string) (string, error) {
	var ip string
	var err error
	if globalIp == "" {
		ip, err = i.pool.Allocate(key)
		if err != nil {
			return "", err
		}
		klog.V(log.DEBUG).Infof("Allocating GlobalIp %s to %s ", ip, key)
		return ip, nil
	}
	givenIp, err := i.pool.RequestIp(key, globalIp)
	if err != nil {
		return "", err
	}
	if globalIp != givenIp {
		// This resource has been allocated a different IP
		klog.V(log.DEBUG).Infof("Updating GlobalIp for %s from %s to %s", key, globalIp, givenIp)
		return givenIp, nil
	}
	return "", nil
}

func (i *Controller) serviceGetter(namespace, name string) (runtime.Object, error) {
	return i.kubeClientSet.CoreV1().Services(namespace).Get(name, metav1.GetOptions{})
}

func (i *Controller) podGetter(namespace, name string) (runtime.Object, error) {
	return i.kubeClientSet.CoreV1().Pods(namespace).Get(name, metav1.GetOptions{})
}

func (i *Controller) serviceUpdater(obj runtime.Object, key string) error {
	service := obj.(*k8sv1.Service)
	existingGlobalIp := service.GetAnnotations()[submarinerIpamGlobalIp]
	allocatedIp, err := i.annotateGlobalIp(key, existingGlobalIp)
	if err != nil { // failed to get globalIp or failed to update, we want to retry
		logAndRequeue(key, i.serviceWorkqueue)
		return fmt.Errorf("failed to annotate GlobalIp to service %s: %v", key, err)
	}

	// This case is hit when the Service does not have the globalIp annotation and a new globalIp is allocated
	// or when the existing annotation on the Service does not match with the allocated globalIp.
	if allocatedIp != "" {
		annotations := service.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[submarinerIpamGlobalIp] = allocatedIp
		service.SetAnnotations(annotations)
		i.syncServiceRules(service, allocatedIp, AddRules)
		_, updateErr := i.kubeClientSet.CoreV1().Services(service.Namespace).Update(service)
		return updateErr
	} else if existingGlobalIp != "" {
		// When Globalnet Controller is migrated, we get notification for all the existing services.
		// For services that already have the annotation, we update the local ipPool cache and sync
		// the iptable rules on the node.
		i.syncServiceRules(service, existingGlobalIp, AddRules)
	}
	return nil
}

func (i *Controller) podUpdater(obj runtime.Object, key string) error {
	pod := obj.(*k8sv1.Pod)
	pod.GetSelfLink()
	existingGlobalIp := pod.GetAnnotations()[submarinerIpamGlobalIp]
	allocatedIp, err := i.annotateGlobalIp(key, existingGlobalIp)
	if err != nil { // failed to get globalIp or failed to update, we want to retry
		logAndRequeue(key, i.podWorkqueue)
		return fmt.Errorf("failed to annotate GlobalIp to Pod %s: %v", key, err)
	}

	// This case is hit when the POD does not have the globalIp annotation and a new globalIp is allocated
	// or when the existing annotation on the POD does not match with the allocated globalIp.
	if allocatedIp != "" {
		annotations := pod.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[submarinerIpamGlobalIp] = allocatedIp
		pod.SetAnnotations(annotations)
		i.syncPodRules(pod.Status.PodIP, allocatedIp, AddRules)
		_, updateErr := i.kubeClientSet.CoreV1().Pods(pod.Namespace).Update(pod)
		return updateErr
	} else if existingGlobalIp != "" {
		// When Globalnet Controller is migrated, we get notification for all the existing PODs.
		// For PODs that already have the annotation, we update the local ipPool cache and sync
		// the iptable rules on the node.
		i.syncPodRules(pod.Status.PodIP, existingGlobalIp, AddRules)
	}
	return nil
}

func logAndRequeue(key string, workqueue workqueue.RateLimitingInterface) {
	klog.V(log.DEBUG).Infof("%s enqueued %d times", key, workqueue.NumRequeues(key))
	workqueue.AddRateLimited(key)
}
