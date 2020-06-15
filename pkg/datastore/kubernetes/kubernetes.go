package kubernetes

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/url"
	"reflect"
	"time"

	"github.com/kelseyhightower/envconfig"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	"github.com/submariner-io/submariner/pkg/datastore"
	"github.com/submariner-io/submariner/pkg/log"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
)

type Datastore struct {
	client          *submarinerClientset.Clientset
	informerFactory submarinerInformers.SharedInformerFactory

	thisClusterID   string
	remoteNamespace string

	stopCh <-chan struct{}
}

type datastoreSpecification struct {
	APIServer       string
	APIServerToken  string
	RemoteNamespace string
	Insecure        bool `default:"false"`
	Ca              string
}

// defaultPrivate indicates whether to start with a private TLS chain
var defaultPrivate bool = false

func NewDatastore(thisClusterID string, stopCh <-chan struct{}) (datastore.Datastore, error) {
	k8sSpec := datastoreSpecification{}

	err := envconfig.Process("broker_k8s", &k8sSpec)
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("https://%s", k8sSpec.APIServer)

	klog.V(log.DEBUG).Infof("Rendered API server host: %q", host)

	submarinerClient, err := getSubmarinerClientset(host, &k8sSpec)
	if err != nil {
		return nil, fmt.Errorf("error building submariner clientset: %v", err)
	}

	return &Datastore{
		client: submarinerClient,
		informerFactory: submarinerInformers.NewSharedInformerFactoryWithOptions(submarinerClient, time.Second*30,
			submarinerInformers.WithNamespace(k8sSpec.RemoteNamespace)),
		thisClusterID:   thisClusterID,
		remoteNamespace: k8sSpec.RemoteNamespace,
		stopCh:          stopCh,
	}, nil
}

func getSubmarinerClientset(host string, k8sSpec *datastoreSpecification) (*submarinerClientset.Clientset, error) {
	// We need to check whether we need the provided CA (for a secure connection)
	// For an insecure connection, both cases are equivalent, but we’ll never get the X.509 error
	// First, try without specifying anything (using the system trust store)
	clientset, err := getAndCheckSubmarinerClientset(host, defaultPrivate, k8sSpec)
	if err != nil {
		if urlError, ok := err.(*url.Error); ok {
			if _, ok := urlError.Unwrap().(x509.UnknownAuthorityError); ok {
				// Certificate error, try with the trust chain (and remember for next time)
				defaultPrivate = !defaultPrivate
				clientset, err = getAndCheckSubmarinerClientset(host, defaultPrivate, k8sSpec)
			}
		}
	}
	return clientset, err
}

func getAndCheckSubmarinerClientset(host string, private bool, k8sSpec *datastoreSpecification) (*submarinerClientset.Clientset, error) {
	config, err := getSubmarinerRESTConfig(host, private, k8sSpec)
	if err != nil {
		return nil, err
	}
	clientset, err := submarinerClientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	// Check we can list endpoints
	_, err = clientset.SubmarinerV1().Endpoints(k8sSpec.RemoteNamespace).List(metav1.ListOptions{})
	return clientset, err
}

func getSubmarinerRESTConfig(host string, private bool, k8sSpec *datastoreSpecification) (*rest.Config, error) {
	k8sClientConfig := rest.Config{
		// TODO: switch to using cluster DNS.
		Host:        host,
		BearerToken: k8sSpec.APIServerToken,
	}
	if k8sSpec.Insecure {
		k8sClientConfig.TLSClientConfig = rest.TLSClientConfig{Insecure: true}
	} else if private {
		caDecoded, err := base64.StdEncoding.DecodeString(k8sSpec.Ca)
		if err != nil {
			return nil, fmt.Errorf("error decoding CA data: %v", err)
		}

		k8sClientConfig.TLSClientConfig = rest.TLSClientConfig{CAData: caDecoded}
	} // In other cases, use the default TLS client config

	return &k8sClientConfig, nil
}

func (k *Datastore) GetEndpoints(clusterID string) ([]types.SubmarinerEndpoint, error) {

	k8sEndpoints, err := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).List(metav1.ListOptions{})

	if err != nil {
		return nil, err
	}

	endpoints := []types.SubmarinerEndpoint{}

	for _, endpoint := range k8sEndpoints.Items {
		if endpoint.Spec.ClusterID == clusterID {
			endpoints = append(endpoints, types.SubmarinerEndpoint{Spec: endpoint.Spec})
		}
	}

	return endpoints, nil
}

func (k *Datastore) WatchClusters(ctx context.Context, selfClusterID string, colorCodes []string, onClusterChange datastore.OnClusterChange) error {

	k.informerFactory.Submariner().V1().Clusters().Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			var object *submarinerv1.Cluster
			var ok bool
			klog.V(log.DEBUG).Infof("AddFunc in WatchClusters called")
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
				klog.V(log.DEBUG).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			if selfClusterID != object.Spec.ClusterID {
				utilruntime.HandleError(onClusterChange(&types.SubmarinerCluster{
					ID:   object.Spec.ClusterID,
					Spec: object.Spec,
				}, false))
			}
		},
		UpdateFunc: func(old, obj interface{}) {
			var object *submarinerv1.Cluster
			var ok bool
			klog.V(log.DEBUG).Infof("UpdateFunc in WatchClusters called")
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
				klog.V(log.DEBUG).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			if selfClusterID != object.Spec.ClusterID {
				utilruntime.HandleError(onClusterChange(&types.SubmarinerCluster{
					ID:   object.Spec.ClusterID,
					Spec: object.Spec,
				}, false))
			}
		},
		DeleteFunc: func(obj interface{}) {
			var object *submarinerv1.Cluster
			var ok bool
			klog.V(log.DEBUG).Infof("DeleteFunc in WatchClusters called")
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
				klog.V(log.DEBUG).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			if selfClusterID != object.Spec.ClusterID {
				utilruntime.HandleError(onClusterChange(&types.SubmarinerCluster{
					ID:   object.Spec.ClusterID,
					Spec: object.Spec,
				}, true))
			}
		},
	}, time.Second*30)

	k.informerFactory.Start(k.stopCh)
	return nil
}

func (k *Datastore) WatchEndpoints(ctx context.Context, selfClusterID string, colorCodes []string, onEndpointChange datastore.OnEndpointChange) error {

	k.informerFactory.Submariner().V1().Endpoints().Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			var object *submarinerv1.Endpoint
			var ok bool
			klog.V(log.DEBUG).Infof("AddFunc in WatchEndpoints called")
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
				klog.V(log.DEBUG).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			if selfClusterID != object.Spec.ClusterID {
				utilruntime.HandleError(onEndpointChange(&types.SubmarinerEndpoint{
					Spec: object.Spec,
				}, false))
			}
		},
		UpdateFunc: func(old, obj interface{}) {
			var object *submarinerv1.Endpoint
			var ok bool
			klog.V(log.DEBUG).Infof("UpdateFunc in WatchEndpoints called")
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
				klog.V(log.DEBUG).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			if selfClusterID != object.Spec.ClusterID {
				utilruntime.HandleError(onEndpointChange(&types.SubmarinerEndpoint{
					Spec: object.Spec,
				}, false))
			}
		},
		DeleteFunc: func(obj interface{}) {
			var object *submarinerv1.Endpoint
			var ok bool
			klog.V(log.DEBUG).Infof("DeleteFunc in WatchEndpoints called")
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
				klog.V(log.DEBUG).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
			}

			if selfClusterID != object.Spec.ClusterID {
				utilruntime.HandleError(onEndpointChange(&types.SubmarinerEndpoint{
					Spec: object.Spec,
				}, true))
			}
		},
	}, time.Second*30)

	k.informerFactory.Start(k.stopCh)
	return nil
}

func (k *Datastore) SetCluster(cluster *types.SubmarinerCluster) error {
	klog.V(log.DEBUG).Infof("In SetCluster: %#v", cluster)

	clusterName, err := util.GetClusterCRDName(cluster)
	if err != nil {
		return fmt.Errorf("error extracting the submariner Cluster name from %#v: %v", cluster, err)
	}

	retrievedCluster, err := k.client.SubmarinerV1().Clusters(k.remoteNamespace).Get(clusterName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("error retrieving submariner Cluster object %q from the central datastore: %v", clusterName, err)
		}

		newClusterObject := &submarinerv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterName,
			},
			Spec: cluster.Spec,
		}

		_, err = k.client.SubmarinerV1().Clusters(k.remoteNamespace).Create(newClusterObject)
		if err != nil {
			return fmt.Errorf("error creating submariner Cluster %#v in the central datastore: %v", newClusterObject, err)
		}

		klog.Infof("Successfully created submariner Cluster %q in the central datastore", clusterName)
	} else {
		if reflect.DeepEqual(cluster.Spec, retrievedCluster.Spec) {
			klog.V(log.DEBUG).Infof("Cluster %q matched what we received from datastore - not updating", clusterName)
			return nil
		}

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			result, getErr := k.client.SubmarinerV1().Clusters(k.remoteNamespace).Get(clusterName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("error retrieving submariner Cluster object %q from the central datastore: %v", clusterName, getErr)
			}
			result.Spec = cluster.Spec
			_, updateErr := k.client.SubmarinerV1().Clusters(k.remoteNamespace).Update(result)
			return updateErr
		})

		if retryErr != nil {
			return fmt.Errorf("error updating submariner Cluster object %q in the central datastore: %v", clusterName, retryErr)
		}

		klog.Infof("Successfully updated submariner Cluster %q in the central datastore", clusterName)
	}
	return nil
}

func (k *Datastore) SetEndpoint(endpoint *types.SubmarinerEndpoint) error {
	klog.V(log.DEBUG).Infof("In SetEndpoint: %#v", endpoint)

	endpointName, err := util.GetEndpointCRDName(endpoint)
	if err != nil {
		return fmt.Errorf("error extracting the submariner Endpoint name from %#v: %v", endpoint, err)
	}

	retrievedEndpoint, err := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Get(endpointName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("error retrieving submariner Endpoint object %q from the central datastore: %v", endpointName, err)
		}

		newEndpointObject := &submarinerv1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: endpointName,
			},
			Spec: endpoint.Spec,
		}

		_, err = k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Create(newEndpointObject)
		if err != nil {
			return fmt.Errorf("error creating submariner Endpoint %#v in the central datastore: %v", endpoint, err)
		}

		klog.Infof("Successfully created submariner Endpoint %q in the central datastore", endpointName)
	} else {
		if reflect.DeepEqual(endpoint.Spec, retrievedEndpoint.Spec) {
			klog.V(log.DEBUG).Infof("Endpoint %q matched what we received from datastore - not updating", endpointName)
			return nil
		}

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			result, getErr := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Get(endpointName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("error retrieving submariner Endpoint object %q from the central datastore: %v", endpointName, getErr)
			}

			result.Spec = endpoint.Spec
			_, updateErr := k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Update(result)
			return updateErr
		})

		if retryErr != nil {
			return fmt.Errorf("error updating submariner Endpoint object %q in the central datastore: %v", endpointName, retryErr)
		}

		klog.Infof("Successfully updated submariner Endpoint %q in the central datastore", endpointName)
	}
	return nil
}

func (k *Datastore) RemoveEndpoint(clusterID, cableName string) error {
	endpointName, err := util.GetEndpointCRDNameFromParams(clusterID, cableName)
	if err != nil {
		return fmt.Errorf("error extracting the submariner Endpoint name: %v", err)
	}

	return k.client.SubmarinerV1().Endpoints(k.remoteNamespace).Delete(endpointName, &metav1.DeleteOptions{})
}
