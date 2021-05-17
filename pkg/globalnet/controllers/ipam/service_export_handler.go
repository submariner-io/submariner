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
package ipam

import (
	"context"

	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func (i *Controller) processServiceExport(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	serviceExport := from.(*mcsv1a1.ServiceExport)

	obj, exists, err := i.serviceSyncer.GetResource(serviceExport.Name, serviceExport.Namespace)
	if err != nil {
		klog.Errorf("Failed to get Service for %s/%s: %v", serviceExport.Namespace, serviceExport.Name, err)
		return nil, true
	}

	if !exists {
		return nil, false
	}

	service := obj.(*k8sv1.Service)

	if op == syncer.Delete {
		return nil, i.serviceUnexported(service)
	}

	return i.processService(service, numRequeues, syncer.Update)
}

func (i *Controller) serviceUnexported(service *k8sv1.Service) bool {
	annotations := service.GetAnnotations()
	if _, ok := annotations[SubmarinerIPAMGlobalIP]; ok {
		i.serviceLock.Lock()
		defer i.serviceLock.Unlock()

		i.processRemovedService(service)

		delete(annotations, SubmarinerIPAMGlobalIP)
		service.SetAnnotations(annotations)

		obj, err := resource.ToUnstructured(service)
		if err != nil {
			klog.Error(err.Error())
			return false
		}

		_, err = i.serviceClient.Namespace(service.Namespace).Update(context.TODO(), obj, metav1.UpdateOptions{})
		if err != nil && !errors.IsNotFound(err) {
			klog.Errorf("Error updating Service %s/%s after ServiceExport removed: %v", service.Namespace, service.Name, err)
			return true
		}
	}

	return false
}
