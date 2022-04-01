/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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

package controllers

import (
	"context"
	"fmt"

	"github.com/submariner-io/admiral/pkg/finalizer"
	"github.com/submariner-io/admiral/pkg/resource"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	versioned "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	rest "k8s.io/client-go/rest"
	"k8s.io/klog"
)

func DeleteGlobalnetObjects(smClientSet *versioned.Clientset, cfg *rest.Config) {
	err := smClientSet.SubmarinerV1().ClusterGlobalEgressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		klog.Errorf("Error deleting the clusterGlobalEgressIPs: %v", err)
	}

	err = smClientSet.SubmarinerV1().GlobalEgressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		klog.Errorf("Error deleting the globalEgressIPs: %v", err)
	}

	deleteGlobalIngressIPs(smClientSet, cfg)
}

func deleteGlobalIngressIPs(smClientSet *versioned.Clientset, cfg *rest.Config) {
	defer deleteAllGlobalIngressIPObjs(smClientSet)

	gvr := schema.GroupVersionResource{
		Group:    corev1.SchemeGroupVersion.Group,
		Version:  corev1.SchemeGroupVersion.Version,
		Resource: "services",
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		klog.Errorf("Error creating dynamic client: %v", err)
		return
	}

	giipList, err := smClientSet.SubmarinerV1().GlobalIngressIPs(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Error listing the globalIngressIP objects: %v", err)
	}

	services := dynClient.Resource(gvr)

	for i := range giipList.Items {
		klog.Infof("Deleting the globalnet internal service: %q", giipList.Items[i].Name)

		err = deleteInternalService(&giipList.Items[i], services)
		if err != nil && !apierrors.IsNotFound(err) {
			klog.Errorf("Error deleting the internal service: %v", err)
		}
	}
}

func deleteInternalService(ingressIP *submarinerv1.GlobalIngressIP, services dynamic.NamespaceableResourceInterface) error {
	serviceRef := ingressIP.Spec.ServiceRef
	internalSvc := GetInternalSvcName(serviceRef.Name)
	key := fmt.Sprintf("%s/%s", ingressIP.Namespace, internalSvc)

	service, exists, _ := getService(internalSvc, ingressIP.Namespace, services, scheme.Scheme)
	if exists {
		if err := finalizer.Remove(context.TODO(), resource.ForDynamic(services.Namespace(ingressIP.Namespace)), service,
			InternalServiceFinalizer); err != nil {
			return fmt.Errorf("error while removing the finalizer from globalnet internal service %q", key)
		}

		err := deleteService(ingressIP.Namespace, internalSvc, services)
		if err != nil && !apierrors.IsNotFound(err) {
			klog.Errorf("Error deleting the service %q/%q: %v", ingressIP.Namespace, internalSvc, err)
		}
	}

	return nil
}

func deleteAllGlobalIngressIPObjs(smClientSet *versioned.Clientset) {
	err := smClientSet.SubmarinerV1().GlobalIngressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		klog.Errorf("Error deleting the globalIngressIPs: %v", err)
	}
}
