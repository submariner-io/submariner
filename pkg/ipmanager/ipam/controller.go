package ipam

import (
	"fmt"
	"sync"
	"time"

	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		pool: pool,
	}
	klog.Info("Setting up event handlers")
	config.ServiceInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: ipamController.enqueueService,
		UpdateFunc: func(old, new interface{}) {
			ipamController.enqueueService(new)
		},
		DeleteFunc: ipamController.handleRemovedService,
	}, handlerResync)
	config.PodInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: ipamController.enqueuePod,
		UpdateFunc: func(old, new interface{}) {
			ipamController.handleUpdatePod(old, new)
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
	for i.processNextService() {
	}
}

func (i *Controller) runPodWorker() {
	for i.processNextPod() {
	}
}

func (i *Controller) processNextService() bool {
	obj, shutdown := i.serviceWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer i.serviceWorkqueue.Done(obj)

		key := obj.(string)
		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			i.serviceWorkqueue.Forget(obj)
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}
		if i.excludeNamespaces[ns] {
			i.serviceWorkqueue.Forget(obj)
			return nil
		}

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Retrieve the latest version of Service before attempting update
			// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
			service, err := i.kubeClientSet.CoreV1().Services(ns).Get(name, metav1.GetOptions{})

			if err != nil {
				return fmt.Errorf("error retrieving submariner-ipam-controller service object %s: %v", name, err)
			}
			annotations, err := i.annotateGlobalIp(&service.Name, service.GetAnnotations())
			if err != nil {
				return fmt.Errorf("failed to annotated GlobalIp to service %s", service.Name)
			}
			if annotations != nil {
				service.SetAnnotations(annotations)
				_, updateErr := i.kubeClientSet.CoreV1().Services(ns).Update(service)
				return updateErr
			}
			return nil
		})
		if retryErr != nil {
			i.serviceWorkqueue.Forget(obj)
			return retryErr
		}

		i.serviceWorkqueue.Forget(obj)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (i *Controller) processNextPod() bool {
	obj, shutdown := i.podWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer i.podWorkqueue.Done(obj)

		key := obj.(string)
		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			i.podWorkqueue.Forget(obj)
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}
		if i.excludeNamespaces[ns] {
			i.podWorkqueue.Forget(obj)
			return nil
		}

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Retrieve the latest version of Pod before attempting update
			// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
			pod, err := i.kubeClientSet.CoreV1().Pods(ns).Get(name, metav1.GetOptions{})

			if err != nil {
				return fmt.Errorf("error retrieving submariner-ipam-controller pod object %s: %v", name, err)
			}
			annotations, err := i.annotateGlobalIp(&pod.Name, pod.GetAnnotations())
			if err != nil {
				return fmt.Errorf("failed to annotated GlobalIp to service %s", pod.Name)
			}
			if annotations != nil {
				pod.SetAnnotations(annotations)
				_, updateErr := i.kubeClientSet.CoreV1().Pods(ns).Update(pod)
				return updateErr
			}
			return nil
		})
		if retryErr != nil {
			i.podWorkqueue.Forget(obj)
			return retryErr
		}

		i.podWorkqueue.Forget(obj)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (i *Controller) enqueueService(obj interface{}) {
	if key := i.allowEnqueue(obj); key != "" {
		klog.V(4).Infof("Enqueueing service for ipam controller %v", key)
		i.serviceWorkqueue.AddRateLimited(key)
	}
}

func (i *Controller) enqueuePod(obj interface{}) {
	if key := i.allowEnqueue(obj); key != "" {
		klog.V(4).Infof("Enqueueing pod for ipam controller %v", key)
		i.podWorkqueue.AddRateLimited(key)
	}
}

func (i *Controller) allowEnqueue(obj interface{}) string {
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

func (i *Controller) handleUpdatePod(old interface{}, new interface{}) {
	pod := new.(*k8sv1.Pod)
	oldGlobalIp := old.(*k8sv1.Pod).GetAnnotations()[submarinerIpamGlobalIp]
	newGlobalIp := new.(*k8sv1.Pod).GetAnnotations()[submarinerIpamGlobalIp]
	if oldGlobalIp != newGlobalIp  && newGlobalIp != i.pool.GetAllocatedIp(pod.Name) {
		klog.V(4).Infof("GlobalIp changed from %s to %s for %s", oldGlobalIp, newGlobalIp, old.(*k8sv1.Pod).Name)
		i.enqueuePod(new)
	}
}

func (i *Controller) handleRemovedService(obj interface{}) {
	var service *k8sv1.Service
	var ok bool
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
			i.pool.Release(globalIp)
			klog.V(4).Infof("Released ip %s for service %s", globalIp, service.Name)
		}
	}
}

func (i *Controller) handleRemovedPod(obj interface{}) {
	var pod *k8sv1.Pod
	var ok bool
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
			i.pool.Release(globalIp)
			klog.V(4).Infof("Released ip %s for pod %s", globalIp, pod.Name)
		}
	}
}

func (i* Controller) annotateGlobalIp(name *string, annotations map[string]string) (map[string]string, error) {
	if annotations == nil {
		annotations = map[string]string{}
	}
	var ip string
	globalIp := annotations[submarinerIpamGlobalIp]
	if globalIp== "" {
		ip = i.pool.Allocate(*name)
		annotations[submarinerIpamGlobalIp] = ip
		klog.V(4).Infof("Allocating GlobalIp %s to %s ", ip, *name)
		return annotations, nil
	}
	givenIp := i.pool.RequestIp(*name, globalIp)
	if globalIp != givenIp {
		// This resource has been allocated a different IP
		annotations[submarinerIpamGlobalIp] = givenIp
		klog.V(4).Infof("Updating GlobalIP for %s from %s to %s", *name, globalIp, givenIp)
		return annotations, nil
	}
	return nil, nil

}
