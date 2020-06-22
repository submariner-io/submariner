package tunnel

import (
	"fmt"
	"time"

	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type Controller struct {
	ce                  cableengine.Engine
	kubeClientSet       kubernetes.Interface
	submarinerClientSet submarinerClientset.Interface
	endpointsSynced     cache.InformerSynced

	objectNamespace string

	endpointWorkqueue workqueue.RateLimitingInterface
}

func NewController(objectNamespace string, ce cableengine.Engine, kubeClientSet kubernetes.Interface, submarinerClientSet submarinerClientset.Interface, endpointInformer submarinerInformers.EndpointInformer) *Controller {
	tunnelController := &Controller{
		ce:                  ce,
		kubeClientSet:       kubeClientSet,
		submarinerClientSet: submarinerClientSet,
		endpointsSynced:     endpointInformer.Informer().HasSynced,
		endpointWorkqueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Endpoints"),
		objectNamespace:     objectNamespace,
	}

	endpointInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: tunnelController.enqueueEndpoint,
		UpdateFunc: func(old, new interface{}) {
			tunnelController.enqueueEndpoint(new)
		},
		DeleteFunc: tunnelController.handleRemovedEndpoint,
	}, 60*time.Second)

	return tunnelController
}

func (t *Controller) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting the tunnel controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, t.endpointsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	go wait.Until(t.runWorker, time.Second, stopCh)

	klog.Info("Tunnel controller started")
	<-stopCh
	klog.Info("Tunnel controller stopping")

	return nil
}

func (t *Controller) runWorker() {
	for t.processNextEndpoint() {

	}
}

func (t *Controller) processNextEndpoint() bool {
	key, shutdown := t.endpointWorkqueue.Get()
	if shutdown {
		return false
	}

	err := func() error {
		defer t.endpointWorkqueue.Done(key)

		ns, name, err := cache.SplitMetaNamespaceKey(key.(string))
		if err != nil {
			return fmt.Errorf("error splitting meta namespace key for endpoint %s: %v", key, err)
		}

		endpoint, err := t.submarinerClientSet.SubmarinerV1().Endpoints(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			t.endpointWorkqueue.Forget(key)
			return fmt.Errorf("error retrieving submariner Endpoint %q: %v", name, err)
		}

		klog.V(log.DEBUG).Infof("Tunnel controller processing added or updated submariner Endpoint object: %#v", endpoint)

		myEndpoint := types.SubmarinerEndpoint{
			Spec: endpoint.Spec,
		}

		err = t.ce.InstallCable(myEndpoint)
		if err != nil {
			t.endpointWorkqueue.AddRateLimited(key)
			return fmt.Errorf("error installing cable for Endpoint %#v, %v", myEndpoint, err)
		}

		t.endpointWorkqueue.Forget(key)
		klog.V(log.DEBUG).Infof("Tunnel controller successfully installed Endpoint cable %s in the engine", endpoint.Spec.CableName)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Tunnel controller failed to process submariner Endpoint with key %q: %v", key, err))
	}

	return true
}

func (t *Controller) enqueueEndpoint(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}

	klog.V(log.TRACE).Infof("Tunnel controller enqueueing Endpoint %v", obj)
	t.endpointWorkqueue.AddRateLimited(key)
}

func (t *Controller) handleRemovedEndpoint(obj interface{}) {
	var object *v1.Endpoint
	var ok bool
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
		klog.V(log.DEBUG).Infof("Tunnel controller recovered deleted Endpoint %q from tombstone", object.Name)
	}

	klog.V(log.DEBUG).Infof("Tunnel controller processing removed submariner Endpoint object: %#v", object)
	myEndpoint := types.SubmarinerEndpoint{
		Spec: object.Spec,
	}

	if err := t.ce.RemoveCable(myEndpoint); err != nil {
		utilruntime.HandleError(fmt.Errorf("Tunnel controller failed to remove Endpoint cable %#v from the engine: %v",
			myEndpoint, err))
		return
	}

	klog.V(log.DEBUG).Infof("Tunnel controller successfully removed Endpoint cable %s from the engine", object.Spec.CableName)
}
