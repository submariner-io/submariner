package ipam

import (
	"fmt"
	"sync"
	"time"

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

func NewController(spec *SubmarinerIpamControllerSpecification, config *InformerConfigStruct) (*Controller, error) {
	exclusionMap := make(map[string]bool)
	for _, v := range spec.ExcludeNS {
		exclusionMap[v] = true
	}
	pool, err := NewIpPool(spec.GlobalCIDR)
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
	var wg sync.WaitGroup
	wg.Add(1)
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting IPAM Controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, i.servicesSynced, i.podsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	go wait.Until(i.runServiceWorker, time.Second, stopCh)
	go wait.Until(i.runPodWorker, time.Second, stopCh)
	wg.Wait()
	<-stopCh
	klog.Info("Shutting down workers")

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
		klog.V(4).Infof("Enqueueing %v for ipam controller", key)
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
		klog.V(4).Infof("GlobalIp changed from %s to %s for %s", oldGlobalIp, newGlobalIp, key)
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
	oldGlobalIp := old.(*k8sv1.Pod).GetAnnotations()[submarinerIpamGlobalIp]
	newGlobalIp := newObj.(*k8sv1.Pod).GetAnnotations()[submarinerIpamGlobalIp]
	if oldGlobalIp != newGlobalIp && newGlobalIp != i.pool.GetAllocatedIp(key) {
		klog.V(4).Infof("GlobalIp changed from %s to %s for %s", oldGlobalIp, newGlobalIp, key)
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
			klog.V(4).Infof("Released ip %s for service %s", globalIp, key)
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
		if globalIp != "" {
			if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
				utilruntime.HandleError(err)
				return
			}
			i.pool.Release(key)
			klog.V(4).Infof("Released ip %s for pod %s", globalIp, key)
		}
	}
}

func (i *Controller) annotateGlobalIp(key string, annotations map[string]string) (map[string]string, error) {
	if annotations == nil {
		annotations = map[string]string{}
	}
	var ip string
	var err error
	globalIp := annotations[submarinerIpamGlobalIp]
	if globalIp == "" {
		ip, err = i.pool.Allocate(key)
		if err != nil {
			return nil, err
		}
		annotations[submarinerIpamGlobalIp] = ip
		klog.V(4).Infof("Allocating GlobalIp %s to %s ", ip, key)
		return annotations, nil
	}
	givenIp, err := i.pool.RequestIp(key, globalIp)
	if err != nil {
		return nil, err
	}
	if globalIp != givenIp {
		// This resource has been allocated a different IP
		annotations[submarinerIpamGlobalIp] = givenIp
		klog.V(4).Infof("Updating GlobalIp for %s from %s to %s", key, globalIp, givenIp)
		return annotations, nil
	}
	return nil, nil
}

func (i *Controller) serviceGetter(namespace, name string) (runtime.Object, error) {
	return i.kubeClientSet.CoreV1().Services(namespace).Get(name, metav1.GetOptions{})
}

func (i *Controller) podGetter(namespace, name string) (runtime.Object, error) {
	return i.kubeClientSet.CoreV1().Pods(namespace).Get(name, metav1.GetOptions{})
}

func (i *Controller) serviceUpdater(obj runtime.Object, key string) error {
	service := obj.(*k8sv1.Service)
	annotations, err := i.annotateGlobalIp(key, service.GetAnnotations())
	if err != nil { // failed to get globalIp or failed to update, we want to retry
		logAndRequeue(key, i.serviceWorkqueue)
		return fmt.Errorf("failed to annotate GlobalIp to service %s: %v", key, err)
	}
	if annotations != nil {
		service.SetAnnotations(annotations)
		_, updateErr := i.kubeClientSet.CoreV1().Services(service.Namespace).Update(service)
		return updateErr
	}
	return nil
}

func (i *Controller) podUpdater(obj runtime.Object, key string) error {
	pod := obj.(*k8sv1.Pod)
	pod.GetSelfLink()
	annotations, err := i.annotateGlobalIp(key, pod.GetAnnotations())
	if err != nil { // failed to get globalIp or failed to update, we want to retry
		logAndRequeue(key, i.podWorkqueue)
		return fmt.Errorf("failed to annotate GlobalIp to Pod %s: %v", key, err)
	}
	if annotations != nil {
		pod.SetAnnotations(annotations)
		_, updateErr := i.kubeClientSet.CoreV1().Pods(pod.Namespace).Update(pod)
		return updateErr
	}
	return nil
}

func logAndRequeue(key string, workqueue workqueue.RateLimitingInterface) {
	klog.V(4).Infof("%s enqueued %d times", key, workqueue.NumRequeues(key))
	workqueue.AddRateLimited(key)
}
