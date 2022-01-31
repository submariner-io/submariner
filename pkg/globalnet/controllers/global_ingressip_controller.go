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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/finalizer"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewGlobalIngressIPController(config *syncer.ResourceSyncerConfig, pool *ipam.IPPool) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	klog.Info("Creating GlobalIngressIP controller")

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.Wrap(err, "error creating the IPTablesInterface handler")
	}

	_, gvr, err := util.ToUnstructuredResource(&corev1.Service{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	controller := &globalIngressIPController{
		baseIPAllocationController: newBaseIPAllocationController(pool, iptIface),
		services:                   config.SourceClient.Resource(*gvr),
		scheme:                     config.Scheme,
	}

	_, gvr, err = util.ToUnstructuredResource(&submarinerv1.GlobalIngressIP{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	client := config.SourceClient.Resource(*gvr)

	list, err := client.Namespace(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "error listing the resources")
	}

	for i := range list.Items {
		obj := &list.Items[i]
		gip := &submarinerv1.GlobalIngressIP{}
		_ = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, gip)

		// nolint:wrapcheck  // No need to wrap these errors.
		err = controller.reserveAllocatedIPs(federator, obj, func(reservedIPs []string) error {
			if gip.Spec.Target == submarinerv1.ClusterIPService {
				return controller.ensureInternalServiceExists(gip)
			} else if gip.Spec.Target == submarinerv1.HeadlessServicePod {
				target := gip.GetAnnotations()[headlessSvcPodIP]
				err := controller.iptIface.AddIngressRulesForHeadlessSvcPod(reservedIPs[0], target)
				if err != nil {
					return err
				}

				key, _ := cache.MetaNamespaceKeyFunc(obj)
				return controller.iptIface.AddEgressRulesForHeadlessSVCPods(key, target, reservedIPs[0], globalNetIPTableMark)
			}
			return nil
		})

		if err != nil {
			return nil, err
		}
	}

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "GlobalIngressIP syncer",
		ResourceType:        &submarinerv1.GlobalIngressIP{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     corev1.NamespaceAll,
		RestMapper:          config.RestMapper,
		Federator:           federator,
		Scheme:              config.Scheme,
		Transform:           controller.process,
		ResourcesEquivalent: syncer.AreSpecsEquivalent,
	})

	if err != nil {
		return nil, errors.Wrap(err, "error creating the syncer")
	}

	return controller, nil
}

func (c *globalIngressIPController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	ingressIP := from.(*submarinerv1.GlobalIngressIP)

	klog.Infof("Processing %sd %#v", op, ingressIP)

	switch op {
	case syncer.Create:
		prevStatus := ingressIP.Status
		requeue := c.onCreate(ingressIP)

		return checkStatusChanged(&prevStatus, &ingressIP.Status, ingressIP), requeue
	case syncer.Delete:
		return nil, c.onDelete(ingressIP, numRequeues)
	case syncer.Update:
	}

	return nil, false
}

func (c *globalIngressIPController) onCreate(ingressIP *submarinerv1.GlobalIngressIP) bool {
	// If Ingress GlobalIP is already allocated, simply return.
	if ingressIP.Status.AllocatedIP != "" {
		return false
	}

	key, _ := cache.MetaNamespaceKeyFunc(ingressIP)

	klog.Infof("Allocating global IP for %q", key)

	ips, err := c.pool.Allocate(1)
	if err != nil {
		klog.Errorf("Error allocating IP for %q: %v", key, err)

		ingressIP.Status.Conditions = util.TryAppendCondition(ingressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "IPPoolAllocationFailed",
			Message: fmt.Sprintf("Error allocating a global IP from the pool: %v", err),
		})

		return true
	}

	if ingressIP.Spec.Target == submarinerv1.ClusterIPService {
		serviceRef := ingressIP.Spec.ServiceRef

		service, exists, err := getService(serviceRef.Name, ingressIP.Namespace, c.services, c.scheme)
		if err != nil || !exists {
			_ = c.pool.Release(ips...)

			key := fmt.Sprintf("%s/%s", ingressIP.Namespace, serviceRef.Name)
			if err != nil {
				klog.Errorf("Error retrieving exported Service %q - re-queueing", key)
			} else {
				klog.Warningf("Exported Service %q does not exist yet - re-queueing", key)
			}

			return false
		}

		internalService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      GetInternalSvcName(service.Name),
				Namespace: service.Namespace,
				Labels: map[string]string{
					InternalServiceLabel: service.Name,
				},
				Finalizers: []string{InternalServiceFinalizer},
			},
		}

		extIPs := []string{ips[0]}
		internalService.Spec.Ports = service.Spec.Ports
		internalService.Spec.Selector = service.Spec.Selector
		internalService.Spec.ExternalIPs = extIPs

		_, err = createService(internalService, c.services)
		if err != nil {
			_ = c.pool.Release(ips...)
			key := fmt.Sprintf("%s/%s", internalService.Namespace, internalService.Name)
			klog.Errorf("Failed to create the internal Service %q ", key)

			ingressIP.Status.Conditions = util.TryAppendCondition(ingressIP.Status.Conditions, &metav1.Condition{
				Type:    string(submarinerv1.GlobalEgressIPAllocated),
				Status:  metav1.ConditionFalse,
				Reason:  "InternalServiceCreationFailed",
				Message: err.Error(),
			})

			return false
		}
	} else if ingressIP.Spec.Target == submarinerv1.HeadlessServicePod {
		podIP := ingressIP.GetAnnotations()[headlessSvcPodIP]
		if podIP == "" {
			_ = c.pool.Release(ips...)

			klog.Warningf("%q annotation is missing on %q", headlessSvcPodIP, key)

			return true
		}

		err = c.iptIface.AddIngressRulesForHeadlessSvcPod(ips[0], podIP)
		if err != nil {
			klog.Errorf("Error while programming Service %q ingress rules for Pod: %v", key, err)
			err = errors.WithMessage(err, "Error programming ingress rules")
		} else {
			err = c.iptIface.AddEgressRulesForHeadlessSVCPods(key, podIP, ips[0], globalNetIPTableMark)
			if err != nil {
				_ = c.iptIface.RemoveIngressRulesForHeadlessSvcPod(ips[0], podIP)
				err = errors.WithMessage(err, "Error programming egress rules")
			}
		}

		if err != nil {
			_ = c.pool.Release(ips...)
			ingressIP.Status.Conditions = util.TryAppendCondition(ingressIP.Status.Conditions, &metav1.Condition{
				Type:    string(submarinerv1.GlobalEgressIPAllocated),
				Status:  metav1.ConditionFalse,
				Reason:  "ProgramIPTableRulesFailed",
				Message: err.Error(),
			})

			return true
		}
	}

	ingressIP.Status.AllocatedIP = ips[0]

	ingressIP.Status.Conditions = util.TryAppendCondition(ingressIP.Status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: "Allocated global IP",
	})

	return false
}

// nolint:wrapcheck  // No need to wrap these errors.
func (c *globalIngressIPController) onDelete(ingressIP *submarinerv1.GlobalIngressIP, numRequeues int) bool {
	if ingressIP.Status.AllocatedIP == "" {
		return false
	}

	key, _ := cache.MetaNamespaceKeyFunc(ingressIP)

	if ingressIP.Spec.Target == submarinerv1.ClusterIPService {
		intSvcName := GetInternalSvcName(ingressIP.Spec.ServiceRef.Name)
		klog.Infof("Deleting the service %q/%q created by Globalnet controller", ingressIP.Namespace, intSvcName)

		intSvc, exists, err := getService(intSvcName, ingressIP.Namespace, c.services, c.scheme)
		if err != nil {
			klog.Errorf("Error retrieving the internal service created by Globalnet controller %q: %v", key, err)
			return shouldRequeue(numRequeues)
		}

		if exists {
			if err = finalizer.Remove(context.TODO(), resource.ForDynamic(c.services.Namespace(ingressIP.Namespace)), intSvc,
				InternalServiceFinalizer); err != nil {
				klog.Errorf("Error while removing the finalizer from service %q: %v", key, err)
				return true
			}

			err = deleteService(ingressIP.Namespace, intSvcName, c.services)
			if err != nil {
				klog.Errorf("Error while deleting the internal %q: %v", key, err)
				return true
			}
		}

		if err = c.pool.Release(ingressIP.Status.AllocatedIP); err != nil {
			klog.Errorf("Error while releasing the global IPs for %q: %v", key, err)
		}

		return false
	}

	return c.flushRulesAndReleaseIPs(key, numRequeues, func(allocatedIPs []string) error {
		if ingressIP.Spec.Target == submarinerv1.HeadlessServicePod {
			podIP := ingressIP.GetAnnotations()[headlessSvcPodIP]
			if podIP != "" {
				if err := c.iptIface.RemoveIngressRulesForHeadlessSvcPod(ingressIP.Status.AllocatedIP, podIP); err != nil {
					return err
				}

				return c.iptIface.RemoveEgressRulesForHeadlessSVCPods(key, podIP, ingressIP.Status.AllocatedIP, globalNetIPTableMark)
			}
		}

		return nil
	}, ingressIP.Status.AllocatedIP)
}

func (c *globalIngressIPController) ensureInternalServiceExists(ingressIP *submarinerv1.GlobalIngressIP) error {
	serviceRef := ingressIP.Spec.ServiceRef
	internalSvc := GetInternalSvcName(serviceRef.Name)
	key := fmt.Sprintf("%s/%s", ingressIP.Namespace, internalSvc)

	service, exists, err := getService(internalSvc, ingressIP.Namespace, c.services, c.scheme)
	if err != nil || !exists {
		return fmt.Errorf("internal service created by Globalnet controller %q does not exist", key)
	}

	if len(service.Spec.ExternalIPs) == 0 || service.Spec.ExternalIPs[0] != ingressIP.Status.AllocatedIP {
		// A user is ideally not supposed to modify the external-ip of the Globalnet internal service, but
		// in-case its done accidentally, as part of controller start/re-start scenario, this code will fix
		// the issue by deleting and re-creating the internal service with valid configuration.
		if err := finalizer.Remove(context.TODO(), resource.ForDynamic(c.services.Namespace(ingressIP.Namespace)), service,
			InternalServiceFinalizer); err != nil {
			return fmt.Errorf("error while removing the finalizer from globalnet internal service %q", key)
		}

		_ = deleteService(ingressIP.Namespace, internalSvc, c.services)

		return fmt.Errorf("globalIP assigned to %q does not match with Internal Service ExternalIP", key)
	}

	return nil
}
