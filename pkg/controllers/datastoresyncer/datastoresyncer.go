package datastoresyncer

import (
	"context"
	"fmt"
	submarinerv1 "github.com/rancher/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/rancher/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/rancher/submariner/pkg/client/informers/externalversions/submariner.io/v1"
	"github.com/rancher/submariner/pkg/datastore"
	"github.com/rancher/submariner/pkg/types"
	"github.com/rancher/submariner/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
	"reflect"
	"sync"
	"time"
)

type DatastoreSyncer struct {
	objectNamespace string
	thisClusterID string
	colorCodes []string
	kubeClientSet kubernetes.Interface
	submarinerClientset submarinerClientset.Interface
	submarinerClusterInformer submarinerInformers.ClusterInformer
	submarinerEndpointInformer submarinerInformers.EndpointInformer
	datastore datastore.Datastore
	localCluster types.SubmarinerCluster
	localEndpoint types.SubmarinerEndpoint

	clusterWorkqueue workqueue.RateLimitingInterface
	endpointWorkqueue workqueue.RateLimitingInterface
}

func NewDatastoreSyncer(thisClusterID string, objectNamespace string, kubeClientSet kubernetes.Interface, submarinerClientset submarinerClientset.Interface, submarinerClusterInformer submarinerInformers.ClusterInformer, submarinerEndpointInformer submarinerInformers.EndpointInformer, datastore datastore.Datastore, colorcodes []string, localCluster types.SubmarinerCluster, localEndpoint types.SubmarinerEndpoint) *DatastoreSyncer {
	newDatastoreSyncer := DatastoreSyncer{
		thisClusterID: thisClusterID,
		objectNamespace: objectNamespace,
		kubeClientSet: kubeClientSet,
		submarinerClientset: submarinerClientset,
		datastore: datastore,
		submarinerClusterInformer: submarinerClusterInformer,
		submarinerEndpointInformer: submarinerEndpointInformer,
		clusterWorkqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Clusters"),
		endpointWorkqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Endpoints"),
		colorCodes: colorcodes,
		localCluster: localCluster,
		localEndpoint: localEndpoint,
	}

	submarinerClusterInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: newDatastoreSyncer.enqueueCluster,
		UpdateFunc: func(old, new interface{}) {
			newDatastoreSyncer.enqueueCluster(new)
		},
		DeleteFunc: newDatastoreSyncer.enqueueCluster,
	}, 60 * time.Second)


	submarinerEndpointInformer.Informer().AddEventHandlerWithResyncPeriod(cache.ResourceEventHandlerFuncs{
		AddFunc: newDatastoreSyncer.enqueueEndpoint,
		UpdateFunc: func(old, new interface{}) {
			newDatastoreSyncer.enqueueEndpoint(new)
		},
		DeleteFunc: newDatastoreSyncer.enqueueEndpoint,
	}, 60 * time.Second)

	return &newDatastoreSyncer
}

func (d *DatastoreSyncer) ensureExclusiveEndpoint() {
	klog.V(4).Infof("Ensuring we are the only endpoint active for this cluster")
	endpoints, err := d.datastore.GetEndpoints(d.localCluster.ID)
	if err != nil {
		klog.Fatalf("Error while retrieving endpoints %v", err)
	}

	for _, endpoint := range endpoints {
		if !util.CompareEndpointSpec(endpoint.Spec, d.localEndpoint.Spec) {
			endpointCrdName, err := util.GetEndpointCRDName(endpoint)
			if err != nil {
				klog.Errorf("Error while converting endpoint to CRD Name %s", endpoint.Spec.CableName)
				break
			}
			// we need to remove this endpoint
			klog.V(4).Infof("Found endpoint (%s) that wasn't us but is part of our cluster, triggered delete in central datastore as well as removing CRD", endpointCrdName)
			err = d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Delete(endpointCrdName, &metav1.DeleteOptions{})
			if err != nil {
				klog.Errorf("Error while deleting endpoint CRD for %s: %v", endpointCrdName, err)
			}
			err = d.datastore.RemoveEndpoint(d.localCluster.ID, endpoint.Spec.CableName)
			if err != nil {
				klog.Errorf("Error while removing endpoint in remote datastore for %s: %v", endpoint.Spec.CableName, d.localCluster.ID)
			}
			klog.V(4).Infof("Removed endpoint %s", endpointCrdName)
		}
	}
}

func (d *DatastoreSyncer) enqueueCluster(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(8).Infof("Enqueueing cluster %v", obj)
	d.clusterWorkqueue.AddRateLimited(key)
}

func (d *DatastoreSyncer) enqueueEndpoint(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(8).Infof("Enqueueing endpoint %v", obj)
	d.endpointWorkqueue.AddRateLimited(key)
}

func (d *DatastoreSyncer) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	defer d.clusterWorkqueue.ShutDown()
	defer d.endpointWorkqueue.ShutDown()
	klog.V(4).Infof("Starting the DatastoreSyncer")
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, d.submarinerClusterInformer.Informer().HasSynced, d.submarinerEndpointInformer.Informer().HasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	d.ensureExclusiveEndpoint()

	err := d.reconcileClusterCRD(d.localCluster, false)
	if (err != nil) {
		return fmt.Errorf("Error reconciling local Cluster CRD: %v", err)
	}

	err = d.reconcileEndpointCRD(d.localEndpoint, false)
	if (err != nil) {
		return fmt.Errorf("Error reconciling local Endpoint CRD: %v", err)
	}

	go utilruntime.HandleError(d.datastore.WatchClusters(context.TODO(), d.thisClusterID, d.colorCodes, d.reconcileClusterCRD))
	go utilruntime.HandleError(d.datastore.WatchEndpoints(context.TODO(), d.thisClusterID, d.colorCodes, d.reconcileEndpointCRD))

	klog.Info("Started datastoresyncer workers")

	go wait.Until(d.runClusterWorker, time.Second, stopCh)

	go wait.Until(d.runEndpointWorker, time.Second, stopCh)

	//go wait.Until(d.runReaper, time.Second, stopCh)

	<-stopCh
	klog.Info("Shutting down datastoresyncer workers")
	return nil
}

func (d *DatastoreSyncer) runClusterWorker() {
	for d.processNextClusterWorkItem() {
	}
}

func (d *DatastoreSyncer) processNextClusterWorkItem() bool {
	obj, shutdown := d.clusterWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer d.clusterWorkqueue.Done(obj)
		klog.V(8).Infof("Processing cluster object: %v", obj)
		ns, key, err := cache.SplitMetaNamespaceKey(obj.(string))
		if err != nil {
			klog.Errorf("error while splitting meta namespace key: %v", err)
			return nil
		}
		if d.thisClusterID != key {
			klog.V(6).Infof("The updated cluster object was not for this cluster, skipping updating the datastore")
			// not actually an error but we should forget about this and return
			d.clusterWorkqueue.Forget(obj)
			return nil
		}
		cluster, err := d.submarinerClusterInformer.Lister().Clusters(ns).Get(key)
		if err != nil {
			klog.Errorf("Error while retrieving submariner cluster object %s", obj)
			d.clusterWorkqueue.Forget(obj)
			return nil
		}
		myCluster := types.SubmarinerCluster{
			ID: cluster.Name,
			Spec: cluster.Spec,
		}
		klog.V(4).Infof("Attempting to trigger an update of the central datastore with the updated CRD")
		err = d.datastore.SetCluster(myCluster)
		klog.V(4).Infof("Update of cluster in central datastore was successful")
		if err != nil {
			klog.Errorf("There was an error updating the cluster in the central datastore, error: %v", err)
		}
		d.clusterWorkqueue.Forget(obj)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (d *DatastoreSyncer) runEndpointWorker() {
	for d.processNextEndpointWorkItem() {
	}
}

func (d *DatastoreSyncer) processNextEndpointWorkItem() bool {
	obj, shutdown := d.endpointWorkqueue.Get()
	if shutdown {
		return false
	}
	err := func() error {
		defer d.endpointWorkqueue.Done(obj)
		klog.V(8).Infof("Processing endpoint object: %v", obj)
		ns, key, err := cache.SplitMetaNamespaceKey(obj.(string))
		if err != nil {
			klog.Errorf("error while splitting meta namespace key: %v", err)
			return nil
		}
		endpoint, err := d.submarinerClientset.SubmarinerV1().Endpoints(ns).Get(key, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("Error while retrieving submariner endpoint object %s", obj)
			d.endpointWorkqueue.Forget(obj)
			return nil
		}
		if d.thisClusterID != endpoint.Spec.ClusterID {
			klog.V(4).Infof("The updated endpoint object was not for this cluster, skipping updating the datastore")
			// not actually an error but we should forget about this and return
			d.endpointWorkqueue.Forget(obj)
			return nil
		}
		if d.localEndpoint.Spec.CableName != endpoint.Spec.CableName {
			klog.V(4).Infof("This endpoint is not me, not updating central datastore")
			d.endpointWorkqueue.Forget(obj)
			return nil
		}
		myEndpoint := types.SubmarinerEndpoint{
			Spec: endpoint.Spec,
		}
		klog.V(4).Infof("Attempting to trigger an update of the central datastore with the updated endpoint CRD")
		err = d.datastore.SetEndpoint(myEndpoint)
		if err != nil {
			klog.Errorf("There was an error updating the endpoint in the central datastore, error: %v", err)
		} else {
			klog.V(4).Infof("Update of endpoint in central datastore was successful")
		}
		d.endpointWorkqueue.Forget(obj)
		return nil
	}()

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (d *DatastoreSyncer) reconcileClusterCRD(localCluster types.SubmarinerCluster, delete bool) error {
	clusterCRDName, err := util.GetClusterCRDName(localCluster)
	if err != nil {
		return fmt.Errorf("Error converting the Cluster CRD name: %v", err)
	}
	var found bool
	cluster, err := d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Get(clusterCRDName, metav1.GetOptions{})
	if err != nil {
		klog.V(4).Infof("There was an error retrieving the local Cluster CRD for %s, assuming it does not exist and creating a new one. The error was: %v",
		    clusterCRDName, err)
		found = false
	} else {
		found = true
	}

	if delete {
		if found {
			klog.V(6).Infof("Attempting to delete Cluster CRD %s from the local datastore", clusterCRDName)
			err = d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Delete(clusterCRDName, &metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("Error deleting Cluster CRD %s from the local datastore: %v", clusterCRDName, err)
			}
		} else {
			klog.V(6).Infof("Cluster CRD %s was not found for deletion", clusterCRDName)
		}
	} else {
		if !found {
			cluster = &submarinerv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterCRDName,
				},
				Spec: localCluster.Spec,
			}
			_, err = d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Create(cluster)
			if err != nil {
				return fmt.Errorf("Error creating Cluster CRD %s in the local datastore: %v", clusterCRDName, err)
			}
		} else {
			if reflect.DeepEqual(cluster.Spec, localCluster.Spec) {
				klog.V(4).Infof("Cluster CRD matched what we received from datastore, not reconciling")
				return nil
			}
			retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				result, getErr := d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Get(clusterCRDName, metav1.GetOptions{})
				if getErr != nil {
					return fmt.Errorf("Error retrieving latest version of Cluster %s: %v", clusterCRDName, getErr)
				}
				result.Spec = localCluster.Spec
				_, updateErr := d.submarinerClientset.SubmarinerV1().Clusters(d.objectNamespace).Update(result)
				return updateErr
			})
			if retryErr != nil {
				return fmt.Errorf("Error updating cluster CRD %s: %v", clusterCRDName, retryErr)
			}
		}
	}
	return nil
}

func (d *DatastoreSyncer) runReaper() {
	var wg sync.WaitGroup
	klog.V(4).Infof("Starting reaper")
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			clusters, err := d.datastore.GetClusters(d.colorCodes)
			if err != nil {
				klog.Errorf("Error retrieving remote Clusters: %v", err)
				return;
			}
			for _, cluster := range clusters {
				endpoints, err := d.datastore.GetEndpoints(cluster.ID)
				if err != nil {
					klog.Errorf("Error retrieving remote Endpoints for cluster %s: %v", cluster.ID, err)
					continue
				}

				crdEndpoints, err := d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).List(metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Error retrieving local Endpoints: %v", err)
					continue
				}

				for _, crde := range crdEndpoints.Items {
					if util.CompareEndpointSpec(crde.Spec, d.localEndpoint.Spec) {
						klog.V(4).Infof("Not going to delete self from kubernetes")
					} else {
						if searchEndpoints(endpoints, crde.Spec.CableName, crde.Spec.ClusterID) {
							klog.V(4).Infof("Found CRD %s in the API server list of endpoints, not doing anything", crde.Name)
						} else {
							// remove the crde
							if cluster.ID == crde.Spec.ClusterID {
								if reflect.DeepEqual(crde.Spec, d.localEndpoint.Spec) {
									klog.V(4).Infof("Not reaping own endpoint %s", crde.Name)
								} else {
									klog.V(4).Infof("Removing the CRD %s because it was not found in the API server list", crde.Name)
									err = d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Delete(crde.Name, &metav1.DeleteOptions{})
									if err != nil {
										klog.Errorf("Error deleting local CRDE %s: %v", crde.Name, err)
									}
								}
							} else {
								klog.V(4).Infof("CRDE wasn't found in list but did not match the cluster we're searching for right now")
							}
						}
					}
				}

			}
			klog.V(4).Infof("Sleeping for 15 seconds")
			time.Sleep(15 * time.Second)
		}
	}()
	wg.Wait()
	klog.Fatalf("reaper exited")
}

// basic brute force search for now
// returns true if the endpoint was found in the passed in list
func searchEndpoints(endpoints []types.SubmarinerEndpoint, cableName string, clusterID string) bool {
	for _, endpoint := range endpoints {
		if endpoint.Spec.CableName == cableName && endpoint.Spec.ClusterID == clusterID {
			return true
		}
	}
	return false
}

func (d *DatastoreSyncer) reconcileEndpointCRD(rawEndpoint types.SubmarinerEndpoint, delete bool) error {
	endpointName, err := util.GetEndpointCRDName(rawEndpoint)
	if err != nil {
		return fmt.Errorf("Error converting the Enndpoint CRD name: %v", err)
	}

	var found bool
	endpoint, err := d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Get(endpointName, metav1.GetOptions{})
	if err != nil {
		klog.V(4).Infof("There was an error retrieving the local Endpoint CRD for %s, assuming it does not exist and creating a new one. The error was: %v",
		    endpointName, err)
		found = false
	} else {
		found = true
	}

	if delete {
		if found {
			klog.V(6).Infof("Attempting to delete Endpoint CRD %s from local datastore", endpointName)
			err = d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Delete(endpointName, &metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("Error deleting Endpoint CRD %s from the local datastore: %v", endpointName, err)
			}
		} else {
			klog.V(6).Infof("Endpoint CRD %s was not found for deletion", endpointName)
		}
	} else {
		if !found {
			endpoint = &submarinerv1.Endpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name: endpointName,
				},
				Spec: rawEndpoint.Spec,
			}
			_, err = d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Create(endpoint)
			if err != nil {
				return fmt.Errorf("Error creating Endpoint CRD %s in the local datastore: %v", endpointName, err)
			}
		} else {
			if reflect.DeepEqual(endpoint.Spec, rawEndpoint.Spec) {
				klog.V(4).Infof("Endpoint CRD matched what we received from datastore, not reconciling")
				return nil
			}
			retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				result, getErr := d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Get(endpointName, metav1.GetOptions{})
				if getErr != nil {
					return fmt.Errorf("Error retrieving latest version of Endpoint %s: %v", endpointName, getErr)
				}
				result.Spec = rawEndpoint.Spec
				_, updateErr := d.submarinerClientset.SubmarinerV1().Endpoints(d.objectNamespace).Update(result)
				return updateErr
			})
			if retryErr != nil {
				return fmt.Errorf("Error updating endpoint CRD %s: %v", endpointName, retryErr)
			}
		}
	}
	return nil
}

