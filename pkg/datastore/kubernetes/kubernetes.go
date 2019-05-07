package kubernetes

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/kelseyhightower/envconfig"
	submarinerv1 "github.com/rancher/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/rancher/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/rancher/submariner/pkg/client/informers/externalversions"
	"github.com/rancher/submariner/pkg/types"
	"github.com/rancher/submariner/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
	"reflect"
	"time"
)

type k8s struct {
	client *submarinerClientset.Clientset
	informerFactory submarinerInformers.SharedInformerFactory

	thisClusterID string
	remoteNamespace string

	stopCh <-chan struct{}
}

type k8sSpecification struct {
	ApiServer string
	ApiServerToken string
	RemoteNamespace string
	Insecure bool `default:"false"`
	Ca string
}

func NewK8sDatastore(thisClusterID string, stopCh <-chan struct{}) *k8s {
	kubeDatastore := k8s{
		thisClusterID: thisClusterID,
		stopCh: stopCh,
	}

	k8sSpec := k8sSpecification{}

	err := envconfig.Process("broker_k8s", &k8sSpec)
	if err != nil {
		klog.Fatal(err)
	}

	kubeDatastore.remoteNamespace = k8sSpec.RemoteNamespace
	host := fmt.Sprintf("https://%s", k8sSpec.ApiServer)
	klog.V(6).Infof("Rendered host for access was: %s", host)
	tlsClientConfig := rest.TLSClientConfig{}
	if k8sSpec.Insecure {
		tlsClientConfig.Insecure = true
	} else {
		caDecoded, err := base64.StdEncoding.DecodeString(k8sSpec.Ca)
		if err != nil {
			klog.Fatalf("error decoding CA data: %v", err)
		}
		tlsClientConfig.CAData = caDecoded
	}
	k8sClientConfig := rest.Config{
		// TODO: switch to using cluster DNS.
		Host:            host,
		TLSClientConfig: tlsClientConfig,
		BearerToken: k8sSpec.ApiServerToken,
	}

	submarinerClient, err := submarinerClientset.NewForConfig(&k8sClientConfig)
	if err != nil {
		klog.Fatalf("Error building submariner clientset: %s", err.Error())
	}
	kubeDatastore.client = submarinerClient
	kubeDatastore.informerFactory = submarinerInformers.NewSharedInformerFactoryWithOptions(submarinerClient, time.Second*30, submarinerInformers.WithNamespace(k8sSpec.RemoteNamespace))
	return &kubeDatastore
}

// todo: need to fix this O(n^2) solution...
func stringSliceOverlaps(left []string, right []string) bool {
	for _, s := range left {
		for _, t := range right {
			if s == t {
				return true
			}
		}
	}
	return false
}

func (k *k8s) GetClusters(colorCodes []string) ([]types.SubmarinerCluster, error) {
	var clusters []types.SubmarinerCluster

	k8sClusters, err := k.client.SubmarinerV1().Clusters(k.remoteNamespace).List(metav1.ListOptions{})

	if err != nil {
		return nil, err
	}

	for _, cluster := range k8sClusters.Items {
		if stringSliceOverlaps(cluster.Spec.ColorCodes, colorCodes) {
			clusters = append(clusters, types.SubmarinerCluster{
				ID: cluster.Spec.ClusterID,
				Spec: cluster.Spec,
			}) // this is likely going to add duplicate clusters
		}
	}
	return clusters, nil
}
func (k *k8s) GetCluster(clusterId string) (types.SubmarinerCluster, error) {

	k8sClusters, err := k.client.SubmarinerV1().Clusters(k.remoteNamespace).List(metav1.ListOptions{})

	if err != nil {
		return types.SubmarinerCluster{}, err
	}

	for _, cluster := range k8sClusters.Items {
		if cluster.Spec.ClusterID == clusterId {
			return types.SubmarinerCluster{
				ID: clusterId,
				Spec: cluster.Spec,
			}, nil
		}
	}

	return types.SubmarinerCluster{}, fmt.Errorf("cluster wasn't found")
}
func (k *k8s) GetEndpoints(clusterId string) ([]types.SubmarinerEndpoint, error) {

	k8sEndpoints, err := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).List(metav1.ListOptions{})

	if err != nil {
		return nil, err
	}

	endpoints := []types.SubmarinerEndpoint{}

	for _, endpoint := range k8sEndpoints.Items {
		if endpoint.Spec.ClusterID == clusterId {
			endpoints = append(endpoints, types.SubmarinerEndpoint{Spec: endpoint.Spec})
		}
	}

	return endpoints, nil
}
func (k *k8s) GetEndpoint(clusterId string, cableName string) (types.SubmarinerEndpoint, error) {

	k8sEndpoints, err := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).List(metav1.ListOptions{})

	if err != nil {
		return types.SubmarinerEndpoint{}, err
	}

	for _, endpoint := range k8sEndpoints.Items {
		if endpoint.Spec.ClusterID == clusterId && endpoint.Spec.CableName == cableName {
			return types.SubmarinerEndpoint{Spec: endpoint.Spec}, nil
		}
	}
	return types.SubmarinerEndpoint{}, fmt.Errorf("endpoint wasn't found")
}
func (k *k8s) WatchClusters(ctx context.Context, selfClusterId string, colorCodes []string, onClusterChange func(cluster types.SubmarinerCluster, deleted bool) error) error {

	k.informerFactory.Submariner().V1().Clusters().Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func (obj interface{}) {
			var object *submarinerv1.Cluster
			var ok bool
			klog.V(8).Infof("AddFunc in WatchClusters called")
			if object, ok = obj.(*submarinerv1.Cluster); !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("Could not convert object %v to a Cluster", obj)
					return
				}
				object, ok = tombstone.Obj.(*submarinerv1.Cluster)
				if !ok {
					klog.Errorf("Could not convert object tombstone %v to a Cluster", tombstone.Obj)
					return
				}
				klog.V(6).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			utilruntime.HandleError(onClusterChange(types.SubmarinerCluster{
				ID: object.Spec.ClusterID,
				Spec: object.Spec,
			}, false))
		},
		UpdateFunc: func (old, obj interface{}) {
			var object *submarinerv1.Cluster
			var ok bool
			klog.V(8).Infof("UpdateFunc in WatchClusters called")
			if object, ok = obj.(*submarinerv1.Cluster); !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("Could not convert object %v to a Cluster", obj)
					return
				}
				object, ok = tombstone.Obj.(*submarinerv1.Cluster)
				if !ok {
					klog.Errorf("Could not convert object tombstone %v to a Cluster", tombstone.Obj)
					return
				}
				klog.V(6).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			utilruntime.HandleError(onClusterChange(types.SubmarinerCluster{
				ID: object.Spec.ClusterID,
				Spec: object.Spec,
			}, false))
		},
		DeleteFunc: func (obj interface{}) {
			var object *submarinerv1.Cluster
			var ok bool
			klog.V(8).Infof("DeleteFunc in WatchClusters called")
			if object, ok = obj.(*submarinerv1.Cluster); !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("Could not convert object %v to a Cluster", obj)
					return
				}
				object, ok = tombstone.Obj.(*submarinerv1.Cluster)
				if !ok {
					klog.Errorf("Could not convert object tombstone %v to a Cluster", tombstone.Obj)
					return
				}
				klog.V(6).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			utilruntime.HandleError(onClusterChange(types.SubmarinerCluster{
				ID: object.Spec.ClusterID,
				Spec: object.Spec,
			}, true))
		},
	}, time.Second * 30)

	k.informerFactory.Start(k.stopCh)
	return nil
}
func (k *k8s) WatchEndpoints(ctx context.Context, selfClusterId string, colorCodes []string, onEndpointChange func (endpoint types.SubmarinerEndpoint, deleted bool) error) error {

	k.informerFactory.Submariner().V1().Endpoints().Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func (obj interface{}) {
			var object *submarinerv1.Endpoint
			var ok bool
			klog.V(8).Infof("AddFunc in WatchEndpoints called")
			if object, ok = obj.(*submarinerv1.Endpoint); !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("Could not convert object %v to an Endpoint", obj)
					return
				}
				object, ok = tombstone.Obj.(*submarinerv1.Endpoint)
				if !ok {
					klog.Errorf("Could not convert object tombstone %v to an Endpoint", tombstone.Obj)
					return
				}
				klog.V(6).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			utilruntime.HandleError(onEndpointChange(types.SubmarinerEndpoint{
				Spec: object.Spec,
			}, false))
		},
		UpdateFunc: func (old, obj interface{}) {
			var object *submarinerv1.Endpoint
			var ok bool
			klog.V(8).Infof("UpdateFunc in WatchEndpoints called")
			if object, ok = obj.(*submarinerv1.Endpoint); !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("Could not convert object %v to an Endpoint", obj)
					return
				}
				object, ok = tombstone.Obj.(*submarinerv1.Endpoint)
				if !ok {
					klog.Errorf("Could not convert object tombstone %v to an Endpoint", tombstone.Obj)
					return
				}
				klog.V(6).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			utilruntime.HandleError(onEndpointChange(types.SubmarinerEndpoint{
				Spec: object.Spec,
			}, false))
		},
		DeleteFunc: func (obj interface{}) {
			var object *submarinerv1.Endpoint
			var ok bool
			klog.V(8).Infof("DeleteFunc in WatchEndpoints called")
			if object, ok = obj.(*submarinerv1.Endpoint); !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("Could not convert object %v to an Endpoint", obj)
					return
				}
				object, ok = tombstone.Obj.(*submarinerv1.Endpoint)
				if !ok {
					klog.Errorf("Could not convert object tombstone %v to an Endpoint", tombstone.Obj)
					return
				}
				klog.V(6).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			utilruntime.HandleError(onEndpointChange(types.SubmarinerEndpoint{
				Spec: object.Spec,
			}, true))
		},
	}, time.Second * 30)

	k.informerFactory.Start(k.stopCh)
	return nil
}
func (k *k8s) SetCluster(cluster types.SubmarinerCluster) error {
	clusterCRDName, err := util.GetClusterCRDName(cluster)
	if err != nil {
		return fmt.Errorf("Error converting the Cluster CRD name: %v", err)
	}

	retrievedCluster, err := k.client.SubmarinerV1().Clusters(k.remoteNamespace).Get(clusterCRDName, metav1.GetOptions{})
	if err != nil {
		klog.V(4).Infof("There was an error retrieving the remote Cluster CRD for %s, assuming it does not exist and creating a new one. The error was: %v",
		    clusterCRDName, err)
		newClusterObject := &submarinerv1.Cluster{
			ObjectMeta: metav1.ObjectMeta {
				Name: clusterCRDName,
			},
			Spec: cluster.Spec,
		}

		_, err = k.client.SubmarinerV1().Clusters(k.remoteNamespace).Create(newClusterObject)
		if err != nil {
			return fmt.Errorf("Error creating Cluster CRD %s in the remote broker: %v", clusterCRDName, err)
		}
	} else {
		if reflect.DeepEqual(cluster.Spec, retrievedCluster.Spec) {
			klog.V(4).Infof("Cluster CRD matched what we received from k8s broker, not reconciling")
			return nil
		}
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			result, getErr := k.client.SubmarinerV1().Clusters(k.remoteNamespace).Get(clusterCRDName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("Error retrieving latest version of Cluster %s: %v", clusterCRDName, getErr)
			}
			result.Spec = cluster.Spec
			_, updateErr := k.client.SubmarinerV1().Clusters(k.remoteNamespace).Update(result)
			return updateErr
		})
		if retryErr != nil {
			return fmt.Errorf("Error updating Cluster CRD %s in remote broker: %v", clusterCRDName, retryErr)
		}
	}
	return nil
}
func (k *k8s) SetEndpoint(endpoint types.SubmarinerEndpoint) error {
	endpointCRDName, err := util.GetEndpointCRDName(endpoint)
	if err != nil {
		return fmt.Errorf("Error converting the Endpoint CRD name: %v", err)
	}

	retrievedEndpoint, err := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Get(endpointCRDName, metav1.GetOptions{})
	if err != nil {
		klog.V(4).Infof("There was an error retrieving the local Endpoint CRD for %s, assuming it does not exist and creating a new one. The error was: %v",
		    endpointCRDName, err)
		newEndpointObject := &submarinerv1.Endpoint{
			ObjectMeta: metav1.ObjectMeta {
				Name: endpointCRDName,
			},
			Spec: endpoint.Spec,
		}

		_, err = k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Create(newEndpointObject)
		if err != nil {
			return fmt.Errorf("Error creating Endpoint CRD %s in the remote broker: %v", endpointCRDName, err)
		}
	} else {
		if reflect.DeepEqual(endpoint.Spec, retrievedEndpoint.Spec) {
			klog.V(4).Infof("Endpoint CRD matched what we received from k8s broker, not reconciling")
			return nil
		}
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			result, getErr := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Get(endpointCRDName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("Error retrieving latest version of Endpoint %s: %v", endpointCRDName, getErr)
			}
			result.Spec = endpoint.Spec
			_, updateErr := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Update(result)
			return updateErr
		})
		if retryErr != nil {
			return fmt.Errorf("Error updating endpoint CRD %s: %v", endpointCRDName, retryErr)
		}
	}
	return nil
}
func (k *k8s) RemoveEndpoint(clusterId, cableName string) error {
	endpointName, err := util.GetEndpointCRDNameFromParams(clusterId, cableName)

	if err != nil {
		return fmt.Errorf("Error converting the Endpoint CRD name: %v", err)
	}

	return k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Delete(endpointName, &metav1.DeleteOptions{})
}
func (k *k8s) RemoveCluster(clusterId string) error {
	// not implemented yet
	return nil
}