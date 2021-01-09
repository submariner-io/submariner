/*
© 2021 Red Hat, Inc. and others

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
package ipam

import (
	"fmt"
	"time"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/iptables"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"

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

func NewController(spec *SubmarinerIpamControllerSpecification, config *InformerConfigStruct,
	globalCIDR, gwNodeName string) (*Controller, error) {
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
		nodeWorkqueue:    workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Nodes"),
		nodesSynced:      config.NodeInformer.Informer().HasSynced,
		gwNodeName:       gwNodeName,

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
	config.NodeInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ipamController.enqueueObject(obj, ipamController.nodeWorkqueue)
		},
		UpdateFunc: func(old, newObj interface{}) {
			ipamController.handleUpdateNode(old, newObj)
		},
		DeleteFunc: ipamController.handleRemovedNode,
	}, handlerResync)

	return ipamController, nil
}

func (i *Controller) Start(stopCh <-chan struct{}) error {
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
	go wait.Until(i.runNodeWorker, time.Second, stopCh)

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

func (i *Controller) runNodeWorker() {
	for i.processNextObject(i.nodeWorkqueue, i.nodeGetter, i.nodeUpdater) {
	}
}

func (i *Controller) processNextObject(objWorkqueue workqueue.RateLimitingInterface, objGetter func(namespace, name string) (runtime.Object,
	error), objUpdater func(runtimeObj runtime.Object, key string) error) bool {
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
				return fmt.Errorf("error retrieving submariner-ipam-controller object %s: %v", key, err)
			}

			switch runtimeObj := runtimeObj.(type) {
			case *k8sv1.Service:
				switch i.evaluateService(runtimeObj) {
				case Ignore:
					objWorkqueue.Forget(obj)
					return nil
				case Requeue:
					// If the kubeproxy chain for the service is missing on the node (might happen if there is
					// some issue with kubeproxy pod itself or if using non-iptables based kubeproxy), instead
					// of re-queuing forever, we place a hard-limit of maxServiceRequeues.
					if objWorkqueue.NumRequeues(obj) >= maxServiceRequeues {
						objWorkqueue.Forget(obj)
						klog.Warningf("Service %s requeued max(%q) allowed iterations, ignoring it",
							key, maxServiceRequeues)
						return nil
					}
					objWorkqueue.AddRateLimited(obj)
					return fmt.Errorf("service %s requeued %d times", key, objWorkqueue.NumRequeues(obj))
				}
			case *k8sv1.Pod:
				// Process pod event only when it has an ipaddress. Pods skipped here will be handled subsequently
				// during podUpdate event when an ipaddress is assigned to it.
				if runtimeObj.Status.PodIP == "" {
					objWorkqueue.Forget(obj)
					return nil
				}

				// Privileged pods that use hostNetwork will be ignored.
				if runtimeObj.Spec.HostNetwork {
					klog.V(log.DEBUG).Infof("Ignoring pod %q on host %q as it uses hostNetworking", key, runtimeObj.Status.PodIP)
					return nil
				}
			case *k8sv1.Node:
				switch i.evaluateNode(runtimeObj) {
				case Ignore:
					objWorkqueue.Forget(obj)
					return nil
				case Requeue:
					objWorkqueue.AddRateLimited(obj)
					return fmt.Errorf("Node %s requeued %d times", key, objWorkqueue.NumRequeues(obj))
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

func (i *Controller) enqueueObject(obj interface{}, queue workqueue.RateLimitingInterface) {
	if key := i.getEnqueueKey(obj); key != "" {
		klog.V(log.TRACE).Infof("Enqueueing %v for ipam controller", key)
		queue.AddRateLimited(key)
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

func (i *Controller) handleUpdateService(old, newObj interface{}) {
	//TODO: further minimize duplication between this and handleUpdatePod
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(newObj); err != nil {
		utilruntime.HandleError(err)
		return
	}

	service := newObj.(*k8sv1.Service)
	if i.excludeNamespaces[service.Namespace] {
		klog.V(log.DEBUG).Infof("In handleUpdateService, skipping Service %q as it belongs to excluded namespace.", key)
		return
	}

	if !i.isServiceSupported(service) {
		klog.V(log.DEBUG).Infof("In handleUpdateService, skipping Service %q.", key)
		return
	}

	oldGlobalIp := old.(*k8sv1.Service).GetAnnotations()[SubmarinerIpamGlobalIp]
	newGlobalIp := newObj.(*k8sv1.Service).GetAnnotations()[SubmarinerIpamGlobalIp]
	if oldGlobalIp != newGlobalIp && newGlobalIp != i.pool.GetAllocatedIp(key) {
		klog.V(log.DEBUG).Infof("GlobalIp changed from %s to %s for Service %q", oldGlobalIp, newGlobalIp, key)
		i.serviceWorkqueue.Add(key)

		return
	}

	if newGlobalIp == "" {
		klog.Warningf("In handleUpdateService, Service %s does not have globalIp annotation yet.", key)
	}
}

func (i *Controller) handleUpdatePod(old, newObj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(newObj); err != nil {
		utilruntime.HandleError(err)
		return
	}

	if i.excludeNamespaces[newObj.(*k8sv1.Pod).Namespace] {
		klog.V(log.DEBUG).Infof("In handleUpdatePod, skipping pod %q as it belongs to excluded namespace.", key)
		return
	}

	// Ignore privileged pods that use hostNetwork
	if newObj.(*k8sv1.Pod).Spec.HostNetwork {
		klog.V(log.DEBUG).Infof("Pod %q on host %q uses hostNetwork, ignoring", key, newObj.(*k8sv1.Pod).Status.HostIP)
		return
	}

	oldPodIp := old.(*k8sv1.Pod).Status.PodIP
	updatedPodIp := newObj.(*k8sv1.Pod).Status.PodIP
	// When the POD is getting terminated, sometimes we get pod update event with podIp removed.
	if oldPodIp != "" && updatedPodIp == "" {
		klog.V(log.DEBUG).Infof("Pod %q with ip %s is being terminated", key, oldPodIp)
		i.handleRemovedPod(old)

		return
	}

	newGlobalIp := newObj.(*k8sv1.Pod).GetAnnotations()[SubmarinerIpamGlobalIp]
	if newGlobalIp == "" {
		// Pod events that are skipped during addEvent are handled here when they are assigned an ipaddress.
		if updatedPodIp != "" {
			klog.V(log.DEBUG).Infof("In handleUpdatePod, pod %q is now assigned %s address, enqueing", key, updatedPodIp)
			i.podWorkqueue.Add(key)

			return
		} else {
			klog.Warningf("In handleUpdatePod, waiting for K8s to assign an IpAddress to pod %s, state %s", key, newObj.(*k8sv1.Pod).Status.Phase)
			return
		}
	}

	oldGlobalIp := old.(*k8sv1.Pod).GetAnnotations()[SubmarinerIpamGlobalIp]
	if oldGlobalIp != newGlobalIp && newGlobalIp != i.pool.GetAllocatedIp(key) {
		klog.V(log.DEBUG).Infof("GlobalIp changed from %s to %s for %s", oldGlobalIp, newGlobalIp, key)
		i.podWorkqueue.Add(key)

		return
	}
}

func (i *Controller) handleUpdateNode(old, newObj interface{}) {
	// Todo minimize the duplication
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(newObj); err != nil {
		utilruntime.HandleError(err)
		return
	}

	oldCniIfaceIpOnNode := old.(*k8sv1.Node).GetAnnotations()[constants.CniInterfaceIP]
	newCniIfaceIpOnNode := newObj.(*k8sv1.Node).GetAnnotations()[constants.CniInterfaceIP]
	if oldCniIfaceIpOnNode == "" && newCniIfaceIpOnNode != "" {
		klog.V(log.DEBUG).Infof("In handleUpdateNode, node %q is now annotated with cniIfaceIP, enqueing", newObj.(*k8sv1.Node).Name)
		i.enqueueObject(newObj, i.nodeWorkqueue)

		return
	}

	oldGlobalIp := old.(*k8sv1.Node).GetAnnotations()[SubmarinerIpamGlobalIp]
	newGlobalIp := newObj.(*k8sv1.Node).GetAnnotations()[SubmarinerIpamGlobalIp]
	if oldGlobalIp != newGlobalIp && newGlobalIp != i.pool.GetAllocatedIp(key) {
		klog.V(log.DEBUG).Infof("GlobalIp changed from %s to %s for %s", oldGlobalIp, newGlobalIp, key)
		i.enqueueObject(newObj, i.nodeWorkqueue)
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
		globalIp := service.Annotations[SubmarinerIpamGlobalIp]
		if globalIp != "" {
			if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
				utilruntime.HandleError(err)
				return
			}

			i.pool.Release(key)
			klog.V(log.DEBUG).Infof("Released ip %s for service %s", globalIp, key)

			err = i.syncServiceRules(service, globalIp, DeleteRules)
			if err != nil {
				klog.Errorf("Error while cleaning up Service %q ingress rules. %v", key, err)
			}
		} else if i.isServiceSupported(service) {
			klog.Errorf("Error: handleRemovedService called for %q, but globalIp annotation is missing.", key)
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
		globalIp := pod.Annotations[SubmarinerIpamGlobalIp]
		if globalIp != "" && pod.Status.PodIP != "" {
			if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
				utilruntime.HandleError(err)
				return
			}

			i.pool.Release(key)
			klog.V(log.DEBUG).Infof("Released globalIp %s for pod %q", globalIp, key)

			err = i.syncPodRules(pod.Status.PodIP, globalIp, DeleteRules)
			if err != nil {
				klog.Errorf("Error while cleaning up Pod egress rules. %v", err)
			}
		} else {
			podKey := pod.Namespace + "/" + pod.Name
			klog.V(log.DEBUG).Infof("handleRemovedPod called for %q, that has globalIp %s and PodIp %s", podKey, globalIp, pod.Status.PodIP)
		}
	}
}

func (i *Controller) annotateGlobalIp(key, globalIp string) (string, error) {
	var ip string
	var err error
	if globalIp == "" {
		ip, err = i.pool.Allocate(key)
		if err != nil {
			return "", err
		}

		return ip, nil
	}

	givenIp, err := i.pool.RequestIp(key, globalIp)
	if err != nil {
		return "", err
	}

	if globalIp != givenIp {
		// This resource has been allocated a different IP
		klog.Warningf("Updating globalIp for %q from %s to %s", key, globalIp, givenIp)
		return givenIp, nil
	}
	// globalIp on the resource is now updated in the local pool
	// This case will be hit either when the gateway is migrated or when the globalNet Pod is restarted
	return "", nil
}

func (i *Controller) serviceGetter(namespace, name string) (runtime.Object, error) {
	return i.kubeClientSet.CoreV1().Services(namespace).Get(name, metav1.GetOptions{})
}

func (i *Controller) podGetter(namespace, name string) (runtime.Object, error) {
	return i.kubeClientSet.CoreV1().Pods(namespace).Get(name, metav1.GetOptions{})
}

func (i *Controller) nodeGetter(namespace, name string) (runtime.Object, error) {
	return i.kubeClientSet.CoreV1().Nodes().Get(name, metav1.GetOptions{})
}

func (i *Controller) serviceUpdater(obj runtime.Object, key string) error {
	service := obj.(*k8sv1.Service)
	existingGlobalIp := service.GetAnnotations()[SubmarinerIpamGlobalIp]
	allocatedIp, err := i.annotateGlobalIp(key, existingGlobalIp)
	if err != nil { // failed to get globalIp or failed to update, we want to retry
		logAndRequeue(key, i.serviceWorkqueue)
		return fmt.Errorf("failed to annotate GlobalIp to service %s: %v", key, err)
	}

	// This case is hit in one of the two situations
	// 1. when the Service does not have the globalIp annotation and a new globalIp is allocated
	// 2. when the current globalIp annotation on the Service does not match with the info maintained by ipPool
	if allocatedIp != "" {
		klog.V(log.DEBUG).Infof("Allocating globalIp %s to Service %q ", allocatedIp, key)
		err = i.syncServiceRules(service, allocatedIp, AddRules)
		if err != nil {
			logAndRequeue(key, i.serviceWorkqueue)
			return err
		}

		annotations := service.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}

		annotations[SubmarinerIpamGlobalIp] = allocatedIp

		service.SetAnnotations(annotations)
		_, err := i.kubeClientSet.CoreV1().Services(service.Namespace).Update(service)
		if err != nil {
			logAndRequeue(key, i.serviceWorkqueue)
			return err
		}
	} else if existingGlobalIp != "" {
		klog.V(log.DEBUG).Infof("Service %q already has globalIp %s annotation, syncing rules", key, existingGlobalIp)
		// When Globalnet Controller is migrated, we get notification for all the existing Services.
		// For Services that already have the annotation, we update the local ipPool cache and sync
		// the iptable rules on the new GatewayNode.
		// Note: This case will also be hit when Globalnet Pod is restarted
		err = i.syncServiceRules(service, existingGlobalIp, AddRules)
		if err != nil {
			logAndRequeue(key, i.serviceWorkqueue)
			return err
		}
	}

	return nil
}

func (i *Controller) podUpdater(obj runtime.Object, key string) error {
	pod := obj.(*k8sv1.Pod)
	pod.GetSelfLink()
	existingGlobalIp := pod.GetAnnotations()[SubmarinerIpamGlobalIp]
	allocatedIp, err := i.annotateGlobalIp(key, existingGlobalIp)
	if err != nil { // failed to get globalIp or failed to update, we want to retry
		logAndRequeue(key, i.podWorkqueue)
		return fmt.Errorf("failed to annotate globalIp to Pod %q: %+v", key, err)
	}

	// This case is hit in one of the two situations
	// 1. when the POD does not have the globalIp annotation and a new globalIp is allocated
	// 2. when the current globalIp annotation on the POD does not match with the info maintained by ipPool
	if allocatedIp != "" {
		klog.V(log.DEBUG).Infof("Allocating globalIp %s to Pod %q ", allocatedIp, key)
		err = i.syncPodRules(pod.Status.PodIP, allocatedIp, AddRules)
		if err != nil {
			logAndRequeue(key, i.podWorkqueue)
			return err
		}

		annotations := pod.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}

		annotations[SubmarinerIpamGlobalIp] = allocatedIp

		pod.SetAnnotations(annotations)
		_, err := i.kubeClientSet.CoreV1().Pods(pod.Namespace).Update(pod)
		if err != nil {
			logAndRequeue(key, i.podWorkqueue)
			return err
		}
	} else if existingGlobalIp != "" {
		klog.V(log.DEBUG).Infof("Pod %q already has globalIp %s annotation, syncing rules", key, existingGlobalIp)
		// When Globalnet Controller is migrated, we get notification for all the existing PODs.
		// For PODs that already have the annotation, we update the local ipPool cache and sync
		// the iptable rules on the new GatewayNode.
		// Note: This case will also be hit when Globalnet Pod is restarted
		err = i.syncPodRules(pod.Status.PodIP, existingGlobalIp, AddRules)
		if err != nil {
			logAndRequeue(key, i.podWorkqueue)
			return err
		}
	}

	return nil
}

func logAndRequeue(key string, queue workqueue.RateLimitingInterface) {
	klog.V(log.DEBUG).Infof("%s enqueued %d times", key, queue.NumRequeues(key))
	queue.AddRateLimited(key)
}
