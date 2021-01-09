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
	"os"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/globalnet/cleanup"
	"github.com/submariner-io/submariner/pkg/iptables"

	"github.com/submariner-io/submariner/pkg/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
)

const defaultResync = 60 * time.Second

func NewGatewayMonitor(spec *SubmarinerIpamControllerSpecification,
	submarinerClient submarinerClientset.Interface, clientSet kubernetes.Interface) (*GatewayMonitor, error) {
	gatewayMonitor := &GatewayMonitor{
		clusterID:         spec.ClusterID,
		endpointWorkqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Endpoints"),
		submarinerInformerFactory: submarinerInformers.NewSharedInformerFactoryWithOptions(submarinerClient,
			time.Second*30, submarinerInformers.WithNamespace(spec.Namespace)),
		submarinerClientSet: submarinerClient,
		kubeClientSet:       clientSet,
		ipamSpec:            spec,
		isGatewayNode:       false,
	}

	endpointInformer := gatewayMonitor.submarinerInformerFactory.Submariner().V1().Endpoints()

	iptableHandler, err := iptables.New()
	if err != nil {
		return nil, err
	}

	gatewayMonitor.ipt = iptableHandler
	gatewayMonitor.endpointsSynced = endpointInformer.Informer().HasSynced

	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		return nil, errors.New("error reading the NODE_NAME from the environment")
	}

	gatewayMonitor.nodeName = nodeName

	endpointInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			gatewayMonitor.enqueueEndpoint(obj)
		},
		UpdateFunc: func(old, newObj interface{}) {
			gatewayMonitor.enqueueEndpoint(newObj)
		},
		DeleteFunc: gatewayMonitor.handleRemovedEndpoint,
	}, handlerResync)

	return gatewayMonitor, nil
}

func (i *GatewayMonitor) Start(stopCh <-chan struct{}) error {
	klog.Info("Starting GatewayMonitor to monitor the active Gateway node in the cluster.")

	i.submarinerInformerFactory.Start(stopCh)

	klog.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(stopCh, i.endpointsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	if err := CreateGlobalNetMarkingChain(i.ipt); err != nil {
		return fmt.Errorf("error while calling createGlobalNetMarkingChain: %v", err)
	}

	klog.Info("Starting endpoint worker")

	go wait.Until(i.runEndpointWorker, time.Second, stopCh)

	return nil
}
func (i *GatewayMonitor) Stop() {
	klog.Info("GatewayMonitor stopping")

	i.syncMutex.Lock()
	i.stopIpamController()
	i.syncMutex.Unlock()
}

func (r *GatewayMonitor) runEndpointWorker() {
	for r.processNextEndpoint() {
	}
}

func (i *GatewayMonitor) processNextEndpoint() bool {
	obj, shutdown := i.endpointWorkqueue.Get()
	if shutdown {
		return false
	}

	err := func() error {
		defer i.endpointWorkqueue.Done(obj)
		key := obj.(string)
		ns, name, err := cache.SplitMetaNamespaceKey(key)
		if err != nil {
			i.endpointWorkqueue.Forget(obj)
			return fmt.Errorf("error while splitting meta namespace key %s: %v", key, err)
		}

		endpoint, err := i.submarinerClientSet.SubmarinerV1().Endpoints(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			i.endpointWorkqueue.Forget(obj)
			return fmt.Errorf("error retrieving submariner endpoint object %s: %v", name, err)
		}

		klog.V(log.DEBUG).Infof("In processNextEndpoint, endpoint info: %+v", endpoint)

		if endpoint.Spec.ClusterID != i.clusterID {
			klog.V(log.DEBUG).Infof("Endpoint %s belongs to a remote cluster", endpoint.Spec.Hostname)

			overlap, err := util.IsOverlappingCIDR(endpoint.Spec.Subnets, i.ipamSpec.GlobalCIDR[0])
			if err != nil {
				// Ideally this case will never hit, as the subnets are valid CIDRs
				klog.Warningf("unable to validate overlapping Service CIDR: %s", err)
			}

			if overlap {
				// When GlobalNet is used, globalCIDRs allocated to the clusters should not overlap.
				// If they overlap, skip the endpoint as its an invalid configuration which is not supported.
				klog.Errorf("GlobalCIDR %q of local cluster %q overlaps with remote cluster %s",
					i.ipamSpec.GlobalCIDR[0], i.ipamSpec.ClusterID, endpoint.Spec.ClusterID)
				i.endpointWorkqueue.Forget(obj)

				return nil
			}

			for _, remoteSubnet := range endpoint.Spec.Subnets {
				MarkRemoteClusterTraffic(i.ipt, remoteSubnet, AddRules)
			}

			i.endpointWorkqueue.Forget(obj)

			return nil
		}

		endpoints, err := i.submarinerClientSet.SubmarinerV1().Endpoints(ns).List(metav1.ListOptions{})
		if err != nil {
			i.endpointWorkqueue.Forget(obj)
			return fmt.Errorf("error retrieving submariner endpoint list %v", err)
		}

		for _, endpoint := range endpoints.Items {
			if endpoint.Spec.ClusterID != i.clusterID {
				for _, remoteSubnet := range endpoint.Spec.Subnets {
					MarkRemoteClusterTraffic(i.ipt, remoteSubnet, AddRules)
				}
			}
		}

		hostname, err := os.Hostname()
		if err != nil {
			klog.Fatalf("Unable to determine hostname: %v", err)
		}

		// If the endpoint hostname matches with our hostname, it implies we are on gateway node
		if endpoint.Spec.Hostname == hostname {
			klog.V(log.DEBUG).Infof("We are now on GatewayNode %s", endpoint.Spec.PrivateIP)

			i.syncMutex.Lock()
			if !i.isGatewayNode {
				i.isGatewayNode = true
				i.initializeIpamController(i.ipamSpec.GlobalCIDR[0], i.nodeName)
			}
			i.syncMutex.Unlock()
		} else {
			klog.V(log.DEBUG).Infof("We are on non-gatewayNode. GatewayNode ip is %s", endpoint.Spec.PrivateIP)
			i.syncMutex.Lock()
			if i.isGatewayNode {
				i.stopIpamController()
				i.isGatewayNode = false
			}
			i.syncMutex.Unlock()
		}

		i.endpointWorkqueue.Forget(obj)

		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (i *GatewayMonitor) enqueueEndpoint(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}

	klog.V(log.TRACE).Infof("Enqueueing endpoint in gatewayMonitor %v", obj)
	i.endpointWorkqueue.AddRateLimited(key)
}

func (i *GatewayMonitor) handleRemovedEndpoint(obj interface{}) {
	var object *v1.Endpoint
	var ok bool
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

		klog.V(log.DEBUG).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}

	klog.V(log.DEBUG).Infof("Informed of removed endpoint for gateway monitor: %v", object)

	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Could not retrieve hostname: %v", err)
	}

	if object.Spec.Hostname == hostname && object.Spec.ClusterID == i.clusterID {
		i.syncMutex.Lock()
		if i.isGatewayNode {
			i.stopIpamController()
			i.isGatewayNode = false
		}
		i.syncMutex.Unlock()
	} else if object.Spec.ClusterID != i.clusterID {
		// Endpoint associated with remote cluster is removed, delete the associated flows.
		for _, remoteSubnet := range object.Spec.Subnets {
			MarkRemoteClusterTraffic(i.ipt, remoteSubnet, DeleteRules)
		}
	}
}

func (i *GatewayMonitor) initializeIpamController(globalCIDR, gwNodeName string) {
	informerFactory := informers.NewSharedInformerFactoryWithOptions(i.kubeClientSet, defaultResync)

	informerConfig := InformerConfigStruct{
		KubeClientSet:   i.kubeClientSet,
		ServiceInformer: informerFactory.Core().V1().Services(),
		PodInformer:     informerFactory.Core().V1().Pods(),
		NodeInformer:    informerFactory.Core().V1().Nodes(),
	}

	klog.V(log.DEBUG).Infof("On Gateway Node, initializing ipamController.")

	ipamController, err := NewController(i.ipamSpec, &informerConfig, globalCIDR, gwNodeName)
	if err != nil {
		klog.Fatalf("Error creating controller: %s", err.Error())
	}

	i.stopProcessing = make(chan struct{})

	informerFactory.Start(i.stopProcessing)

	if err = ipamController.Start(i.stopProcessing); err != nil {
		klog.Fatalf("Error running ipamController: %s", err.Error())
	}

	klog.V(log.DEBUG).Infof("Successfully started the ipamController")
}

func (i *GatewayMonitor) stopIpamController() {
	if i.stopProcessing != nil {
		klog.V(log.DEBUG).Infof("Stopping ipamController")
		close(i.stopProcessing)
		i.stopProcessing = nil

		cleanup.ClearGlobalnetChains(i.ipt)
	}
}
