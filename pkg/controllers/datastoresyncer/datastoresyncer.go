package datastoresyncer

import (
	"context"
	"fmt"
	"reflect"
	"time"

	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/datastore"
	"github.com/submariner-io/submariner/pkg/log"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type DatastoreSyncer struct {
	objectNamespace            string
	thisClusterID              string
	colorCodes                 []string
	kubeClientSet              kubernetes.Interface
	submarinerClientset        submarinerClientset.Interface
	submarinerClusterInformer  submarinerInformers.ClusterInformer
	submarinerEndpointInformer submarinerInformers.EndpointInformer
	datastore                  datastore.Datastore
	localCluster               types.SubmarinerCluster
	localEndpoint              types.SubmarinerEndpoint

	clusterWorkqueue  workqueue.RateLimitingInterface
	endpointWorkqueue workqueue.RateLimitingInterface
}

func NewDatastoreSyncer(thisClusterID string, objectNamespace string, kubeClientSet kubernetes.Interface, submarinerClientset submarinerClientset.Interface, submarinerClusterInformer submarinerInformers.ClusterInformer, submarinerEndpointInformer submarinerInformers.EndpointInformer, datastore datastore.Datastore, colorcodes []string, localCluster types.SubmarinerCluster, localEndpoint types.SubmarinerEndpoint) *DatastoreSyncer {
	newDatastoreSyncer := DatastoreSyncer{
		thisClusterID:              thisClusterID,
		objectNamespace:            objectNamespace,
		kubeClientSet:              kubeClientSet,
		submarinerClientset:        submarinerClientset,
		datastore:                  datastore,
		submarinerClusterInformer:  submarinerClusterInformer,
		submarinerEndpointInformer: submarinerEndpointInformer,
		clusterWorkqueue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Clusters"),
		endpointWorkqueue:          workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Endpoints"),
		colorCodes:                 colorcodes,
		localCluster:               localCluster,
		localEndpoint:              localEndpoint,
	}

	submarinerClusterInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: newDatastoreSyncer.enqueueCluster,
		UpdateFunc: func(old, new interface{}) {
			newDatastoreSyncer.enqueueCluster(new)
		},
		DeleteFunc: newDatastoreSyncer.enqueueCluster,
	}, 60*time.Second)

	submarinerEndpointInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: newDatastoreSyncer.enqueueEndpoint,
		UpdateFunc: func(old, new interface{}) {
			newDatastoreSyncer.enqueueEndpoint(new)
		},
		DeleteFunc: newDatastoreSyncer.enqueueEndpoint,
	}, 60*time.Second)

	return &newDatastoreSyncer
}

func (d *DatastoreSyncer) ensureExclusiveEndpoint() error {
	klog.Info("Ensuring we are the only endpoint active for this cluster")
	endpoints, err := d.datastore.GetEndpoints(d.localCluster.ID)
	if err != nil {
		return fmt.Errorf("error retrieving submariner Endpoints %v", err)
	}

	for _, endpoint := range endpoints {
		if !util.CompareEndpointSpec(endpoint.Spec, d.localEndpoint.Spec) {
			endpointName, err := util.GetEndpointCRDName(&endpoint)
			if err != nil {
				klog.Errorf("error extracting the submariner Endpoint name from %#v: %v", endpoint, err)
				continue
			}

			// we need to remove this endpoint
			err = d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Delete(endpointName, &metav1.DeleteOptions{})
			if err != nil {
				klog.Errorf("error deleting submariner Endpoint %q from the local datastore: %v", endpointName, err)
			}

			err = d.datastore.RemoveEndpoint(d.localCluster.ID, endpoint.Spec.CableName)
			if err != nil {
				klog.Errorf("error removing submariner Endpoint with cable name %q from the central datastore: %v", endpoint.Spec.CableName, err)
			}

			klog.Infof("Successfully deleted existing submariner Endpoint %q", endpointName)
		}
	}

	return nil
}

func (d *DatastoreSyncer) enqueueCluster(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(log.TRACE).Infof("Enqueueing cluster %v", key)
	d.clusterWorkqueue.AddRateLimited(key)
}

func (d *DatastoreSyncer) enqueueEndpoint(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(log.TRACE).Infof("Enqueueing endpoint %v", key)
	d.endpointWorkqueue.AddRateLimited(key)
}

func (d *DatastoreSyncer) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	defer d.clusterWorkqueue.ShutDown()
	defer d.endpointWorkqueue.ShutDown()

	klog.Info("Starting the datastore syncer")

	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, d.submarinerClusterInformer.Informer().HasSynced, d.submarinerEndpointInformer.Informer().HasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	if err := d.ensureExclusiveEndpoint(); err != nil {
		return fmt.Errorf("could not ensure exclusive submariner Endpoint: %v", err)
	}

	klog.Infof("Reconciling local submariner Cluster: %#v ", d.localCluster)
	if err := d.reconcileClusterCRD(&d.localCluster, false); err != nil {
		return fmt.Errorf("error reconciling the local submariner Cluster: %v", err)
	}

	klog.Infof("Reconciling local submariner Endpoint: %#v ", d.localEndpoint)
	if err := d.reconcileEndpointCRD(&d.localEndpoint, false); err != nil {
		return fmt.Errorf("error reconciling the local submariner Endpoint: %v", err)
	}

	go utilruntime.HandleError(d.datastore.WatchClusters(context.TODO(), d.thisClusterID, d.colorCodes, d.reconcileClusterCRD))
	go utilruntime.HandleError(d.datastore.WatchEndpoints(context.TODO(), d.thisClusterID, d.colorCodes, d.reconcileEndpointCRD))

	go wait.Until(d.runClusterWorker, time.Second, stopCh)

	go wait.Until(d.runEndpointWorker, time.Second, stopCh)

	//go wait.Until(d.runReaper, time.Second, stopCh)

	klog.Info("Datastore syncer started")

	<-stopCh
	klog.Info("Datastore syncer stopping")
	return nil
}

func (d *DatastoreSyncer) runClusterWorker() {
	for d.processNextClusterWorkItem() {
	}
}

func (d *DatastoreSyncer) processNextClusterWorkItem() bool {
	key, shutdown := d.clusterWorkqueue.Get()
	if shutdown {
		return false
	}

	err := func() error {
		defer d.clusterWorkqueue.Done(key)

		ns, name, err := cache.SplitMetaNamespaceKey(key.(string))
		if err != nil {
			d.clusterWorkqueue.Forget(key)
			return err
		}

		if d.thisClusterID != name {
			klog.V(log.TRACE).Infof("The updated submariner Cluster %q is not for this cluster, skipping updating the datastore", name)
			// not actually an error but we should forget about this and return
			d.clusterWorkqueue.Forget(key)
			return nil
		}

		cluster, err := d.submarinerClusterInformer.Lister().Clusters(ns).Get(name)
		if err != nil {
			d.clusterWorkqueue.Forget(key)
			return err
		}

		klog.V(log.TRACE).Infof("Processing local submariner Cluster object: %#v", cluster)

		myCluster := types.SubmarinerCluster{
			ID:   cluster.Name,
			Spec: cluster.Spec,
		}

		if err = d.datastore.SetCluster(&myCluster); err != nil {
			d.clusterWorkqueue.Forget(key)
			return err
		}

		d.clusterWorkqueue.Forget(key)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Failed to process submariner Cluster with key %q: %v", key, err))
		return true
	}
	return true
}

func (d *DatastoreSyncer) runEndpointWorker() {
	for d.processNextEndpointWorkItem() {
	}
}

func (d *DatastoreSyncer) processNextEndpointWorkItem() bool {
	key, shutdown := d.endpointWorkqueue.Get()
	if shutdown {
		return false
	}

	err := func() error {
		defer d.endpointWorkqueue.Done(key)

		ns, name, err := cache.SplitMetaNamespaceKey(key.(string))
		if err != nil {
			d.endpointWorkqueue.Forget(key)
			return err
		}

		endpoint, err := d.submarinerClientset.SubmarinerV1().Endpoints(ns).Get(name, metav1.GetOptions{})
		if err != nil {
			d.endpointWorkqueue.Forget(key)
			return err
		}

		if d.thisClusterID != endpoint.Spec.ClusterID {
			klog.V(log.DEBUG).Infof("The updated submariner Endpoint %q is not for this cluster - skipping updating the datastore", endpoint.Spec.ClusterID)
			// not actually an error but we should forget about this and return
			d.endpointWorkqueue.Forget(key)
			return nil
		}

		if d.localEndpoint.Spec.CableName != endpoint.Spec.CableName {
			klog.V(log.DEBUG).Infof("The updated submariner Endpoint with CableName %q is not mine - skipping updating the datastore", endpoint.Spec.CableName)
			d.endpointWorkqueue.Forget(key)
			return nil
		}

		klog.V(log.DEBUG).Infof("Processing local submariner Endpoint object: %#v", endpoint)

		myEndpoint := types.SubmarinerEndpoint{
			Spec: endpoint.Spec,
		}

		if err = d.datastore.SetEndpoint(&myEndpoint); err != nil {
			d.endpointWorkqueue.Forget(key)
			return err
		}

		d.endpointWorkqueue.Forget(key)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Failed to process submariner Endpoint with key %q: %v", key, err))
		return true
	}
	return true
}

func (d *DatastoreSyncer) reconcileClusterCRD(fromCluster *types.SubmarinerCluster, delete bool) error {
	klog.V(log.DEBUG).Infof("In reconcileClusterCRD: %#v", fromCluster)

	clusterName, err := util.GetClusterCRDName(fromCluster)
	if err != nil {
		return fmt.Errorf("error extracting the submariner Cluster name from %#v: %v", fromCluster, err)
	}

	var found bool
	cluster, err := d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Get(clusterName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("error retrieving submariner Cluster object %q from the local datastore: %v", clusterName, err)
		}

		found = false
	} else {
		found = true
	}

	if delete {
		if found {
			err = d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Delete(clusterName, &metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("error deleting submariner Cluster %q from the local datastore: %v", clusterName, err)
			}

			klog.Infof("Successfully deleted submariner Cluster %q in the local datastore", clusterName)
		} else {
			klog.V(log.DEBUG).Infof("Submariner Cluster %q was not found for deletion", clusterName)
		}
	} else {
		if !found {
			cluster = &submarinerv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
				Spec: fromCluster.Spec,
			}

			_, err = d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Create(cluster)
			if err != nil {
				return fmt.Errorf("error creating submariner Cluster %#v in the local datastore: %v", cluster, err)
			}

			klog.Infof("Successfully created submariner Cluster %q in the local datastore", clusterName)
		} else {
			if reflect.DeepEqual(cluster.Spec, fromCluster.Spec) {
				klog.V(log.DEBUG).Infof("Cluster %q matched what we received from datastore - not updating", clusterName)
				return nil
			}

			retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				result, getErr := d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Get(clusterName, metav1.GetOptions{})
				if getErr != nil {
					return fmt.Errorf("error retrieving submariner Cluster object %q from the local datastore: %v", clusterName, getErr)
				}

				result.Spec = fromCluster.Spec
				_, updateErr := d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Update(result)
				return updateErr
			})

			if retryErr != nil {
				return fmt.Errorf("error updating submariner Cluster object %q in the local datastore: %v", clusterName, retryErr)
			}

			klog.Infof("Successfully updated submariner Cluster %q in the local datastore", clusterName)
		}
	}
	return nil
}

func (d *DatastoreSyncer) reconcileEndpointCRD(fromEndpoint *types.SubmarinerEndpoint, delete bool) error {
	klog.V(log.DEBUG).Infof("In reconcileEndpointCRD: %#v", fromEndpoint)

	endpointName, err := util.GetEndpointCRDName(fromEndpoint)
	if err != nil {
		return fmt.Errorf("error extracting the submariner Endpoint name from %#v: %v", fromEndpoint, err)
	}

	var found bool
	endpoint, err := d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Get(endpointName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("error retrieving submariner Endpoint object %q from the local datastore: %v", endpointName, err)
		}

		found = false
	} else {
		found = true
	}

	if delete {
		if found {
			err = d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Delete(endpointName, &metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("error deleting submariner Endpoint %q from the local datastore: %v", endpointName, err)
			}

			klog.Infof("Successfully deleted submariner Endpoint %q in the local datastore", endpointName)
		} else {
			klog.V(log.DEBUG).Infof("Submariner Endpoint %q was not found for deletion", endpointName)
		}
	} else {
		if !found {
			endpoint = &submarinerv1.Endpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name: endpointName,
				},
				Spec: fromEndpoint.Spec,
			}

			_, err = d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Create(endpoint)
			if err != nil {
				return fmt.Errorf("error creating submariner Endpoint %#v in the local datastore: %v", endpoint, err)
			}

			klog.Infof("Successfully created submariner Endpoint %q in the local datastore", endpointName)
		} else {
			if reflect.DeepEqual(endpoint.Spec, fromEndpoint.Spec) {
				klog.V(log.DEBUG).Infof("Endpoint %q matched what we received from datastore - not updating", endpointName)
				return nil
			}

			retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				result, getErr := d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Get(endpointName, metav1.GetOptions{})
				if getErr != nil {
					return fmt.Errorf("error retrieving submariner Endpoint object %q from the local datastore: %v", endpointName, getErr)
				}
				result.Spec = fromEndpoint.Spec
				_, updateErr := d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Update(result)
				return updateErr
			})

			if retryErr != nil {
				return fmt.Errorf("error updating submariner Endpoint object %q in the local datastore: %v", endpointName, retryErr)
			}

			klog.Infof("Successfully updated submariner Endpoint %q in the local datastore", endpointName)
		}
	}
	return nil
}
