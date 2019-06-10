package tunnel

import (
	"fmt"
	"time"

	v1 "github.com/rancher/submariner/pkg/apis/submariner.io/v1"
	"github.com/rancher/submariner/pkg/cableengine"
	submarinerClientset "github.com/rancher/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/rancher/submariner/pkg/client/informers/externalversions/submariner.io/v1"
	"github.com/rancher/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type TunnelController struct {
	ce                  cableengine.CableEngine
	kubeClientSet       kubernetes.Interface
	submarinerClientSet submarinerClientset.Interface
	endpointsSynced     cache.InformerSynced

	objectNamespace string

	endpointWorkqueue workqueue.RateLimitingInterface
}

func NewTunnelController(objectNamespace string, ce cableengine.CableEngine, kubeClientSet kubernetes.Interface, submarinerClientSet submarinerClientset.Interface, endpointInformer submarinerInformers.EndpointInformer) *TunnelController {
	tunnelController := &TunnelController{
		ce:                  ce,
		kubeClientSet:       kubeClientSet,
		submarinerClientSet: submarinerClientSet,
		endpointsSynced:     endpointInformer.Informer().HasSynced,
		endpointWorkqueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Endpoints"),
		objectNamespace:     objectNamespace,
	}
	klog.Info("Setting up event handlers")
	endpointInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: tunnelController.enqueueEndpoint,
		UpdateFunc: func(old, new interface{}) {
			tunnelController.enqueueEndpoint(new)
		},
		DeleteFunc: tunnelController.handleRemovedEndpoint,
	}, 60*time.Second)

	return tunnelController
}

func (t *TunnelController) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Tunnel Controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, t.endpointsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	go wait.Until(t.runWorker, time.Second, stopCh)

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

func (t *TunnelController) runWorker() {
	for t.processNextEndpoint() {

	}
}

func (t *TunnelController) processNextEndpoint() bool {
	obj, shutdown := t.endpointWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer t.endpointWorkqueue.Done(obj)
		klog.V(4).Infof("Processing endpoint object: %v", obj)
		ns, key, err := cache.SplitMetaNamespaceKey(obj.(string))
		if err != nil {
			return fmt.Errorf("error splitting meta namespace key for endpoint %s: %v", obj, err)
		}
		endpoint, err := t.submarinerClientSet.SubmarinerV1().Endpoints(ns).Get(key, metav1.GetOptions{})
		if err != nil {
			t.endpointWorkqueue.Forget(obj)
			return fmt.Errorf("error retrieving submariner endpoint key %s: %v", key, err)
		}
		myEndpoint := types.SubmarinerEndpoint{
			Spec: endpoint.Spec,
		}
		err = t.ce.InstallCable(myEndpoint)
		if err != nil {
			t.endpointWorkqueue.AddRateLimited(obj)
			return fmt.Errorf("error installing cable for endpoint %#v, %v", myEndpoint, err)
		}
		t.endpointWorkqueue.Forget(obj)
		klog.V(4).Infof("endpoint processed by tunnel controller")
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
	}

	return true
}

func (t *TunnelController) enqueueEndpoint(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(6).Infof("Enqueueing endpoint for tunnel controller %v", obj)
	t.endpointWorkqueue.AddRateLimited(key)
}

func (t *TunnelController) handleRemovedEndpoint(obj interface{}) {
	var object *v1.Endpoint
	var ok bool
	klog.V(4).Infof("Handling object in handleEndpoint")
	if object, ok = obj.(*v1.Endpoint); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Could not convert object %v to an Endpoint", obj))
			return
		}
		object, ok = tombstone.Obj.(*v1.Endpoint)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Could not convert object tombstone %v to an Endpoint", tombstone.Obj))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}

	klog.V(4).Infof("Informed of removed endpoint for tunnel controller object: %#v", object)
	if err := t.ce.RemoveCable(object.Spec.CableName); err != nil {
		utilruntime.HandleError(fmt.Errorf("error removing endpoint cable %s from engine: %v",
			object.Spec.CableName, err))
		return
	}

	klog.V(4).Infof("Removed endpoint cable %s from engine", object.Spec.CableName)
}
