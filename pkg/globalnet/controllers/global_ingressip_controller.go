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
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewGlobalIngressIPController(config syncer.ResourceSyncerConfig, pool *ipam.IPPool) (Interface, error) {
	var err error

	klog.Info("Creating GlobalIngressIP controller")

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.WithMessage(err, "error creating the IPTablesInterface handler")
	}

	_, gvr, err := util.ToUnstructuredResource(&corev1.Pod{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	controller := &globalIngressIPController{
		baseIPAllocationController: newBaseIPAllocationController(pool, iptIface),
		pods:                       config.SourceClient.Resource(*gvr),
		config:                     config,
	}

	_, gvr, err = util.ToUnstructuredResource(&submarinerv1.GlobalIngressIP{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	client := config.SourceClient.Resource(*gvr)
	list, err := client.Namespace(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for i := range list.Items {
		obj := &list.Items[i]
		gip := &submarinerv1.GlobalIngressIP{}
		_ = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, gip)
		var target string
		if gip.Spec.Target == submarinerv1.ClusterIPService {
			target = obj.GetAnnotations()[kubeProxyIPTableChainAnnotation]
			if target == "" {
				continue
			}
		} else if gip.Spec.Target == submarinerv1.HeadlessServicePod {
			target = obj.GetAnnotations()[headlessSvcPodIP]
			if target == "" {
				continue
			}
		}

		err = controller.reserveAllocatedIPs(federator, obj, func(reservedIPs []string) error {
			if gip.Spec.Target == submarinerv1.ClusterIPService {
				// TODO handle the following case.
				// Observations: If serviceexport is deleted while the GN pod was down, we are still programing rules
				// However, if service is deleted, the chain will be missing and rules will not be programmed.
				return controller.iptIface.AddIngressRulesForService(reservedIPs[0], target)
			} else if gip.Spec.Target == submarinerv1.HeadlessServicePod {
				_, exists, err := controller.getPod(gip.Spec.PodRef.Name, obj.GetNamespace())
				if err != nil || !exists {
					klog.Infof("Headless Service Pod %s/%s does not exist, deleting the corresponding Ingress object",
						obj.GetNamespace(), gip.Spec.PodRef.Name)
					_ = controller.deleteIngressObject(obj, client)
					return nil
				}

				return controller.iptIface.AddIngressRulesForHeadlessSvcPod(reservedIPs[0], target)
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
		return nil, err
	}

	// TODO - reconcile existing items to handle ServiceExport deleted while we were down

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
		tryAppendStatusCondition(&ingressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "IPPoolAllocationFailed",
			Message: fmt.Sprintf("Error allocating a global IP from the pool: %v", err),
		})

		return true
	}

	if ingressIP.Spec.Target == submarinerv1.ClusterIPService {
		chainName := ingressIP.GetAnnotations()[kubeProxyIPTableChainAnnotation]
		if chainName == "" {
			_ = c.pool.Release(ips...)

			klog.Warningf("%q annotation is missing on %q", kubeProxyIPTableChainAnnotation, key)

			return true
		}

		err = c.iptIface.AddIngressRulesForService(ips[0], chainName)
		if err != nil {
			_ = c.pool.Release(ips...)

			klog.Errorf("Error while programming Service %q ingress rules: %v", key, err)
			tryAppendStatusCondition(&ingressIP.Status.Conditions, &metav1.Condition{
				Type:    string(submarinerv1.GlobalEgressIPAllocated),
				Status:  metav1.ConditionFalse,
				Reason:  "ProgramIPTableRulesFailed",
				Message: fmt.Sprintf("Error programming ingress rules: %v", err),
			})

			return true
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
			_ = c.pool.Release(ips...)

			klog.Errorf("Error while programming Service %q ingress rules for Pod: %v", key, err)
			tryAppendStatusCondition(&ingressIP.Status.Conditions, &metav1.Condition{
				Type:    string(submarinerv1.GlobalEgressIPAllocated),
				Status:  metav1.ConditionFalse,
				Reason:  "ProgramIPTableRulesFailed",
				Message: fmt.Sprintf("Error programming ingress rules: %v", err),
			})

			return true
		}

		err = c.iptIface.AddEgressRulesForHeadlessSVCPods(key, podIP, ips[0], globalNetIPTableMark)
		if err != nil {
			_ = c.pool.Release(ips...)
			if err1 := c.iptIface.RemoveIngressRulesForHeadlessSvcPod(ips[0], podIP); err1 != nil {
				klog.Errorf("Error resetting ingress rules for Headless Service Pod %q: %v", key, err)
			}

			klog.Errorf("Error while programming egress rules for Headless Service Pod %q: %v", key, err)
			tryAppendStatusCondition(&ingressIP.Status.Conditions, &metav1.Condition{
				Type:    string(submarinerv1.GlobalEgressIPAllocated),
				Status:  metav1.ConditionFalse,
				Reason:  "ProgramIPTableRulesFailed",
				Message: fmt.Sprintf("Error programming egress rules: %v", err),
			})

			return true
		}
	}

	ingressIP.Status.AllocatedIP = ips[0]

	tryAppendStatusCondition(&ingressIP.Status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: "Allocated global IP",
	})

	return false
}

func (c *globalIngressIPController) onDelete(ingressIP *submarinerv1.GlobalIngressIP, numRequeues int) bool {
	if ingressIP.Status.AllocatedIP == "" {
		return false
	}

	key, _ := cache.MetaNamespaceKeyFunc(ingressIP)

	return c.flushRulesAndReleaseIPs(key, numRequeues, func(allocatedIPs []string) error {
		if ingressIP.Spec.Target == submarinerv1.ClusterIPService {
			chainName := ingressIP.GetAnnotations()[kubeProxyIPTableChainAnnotation]
			if chainName != "" {
				return c.iptIface.RemoveIngressRulesForService(ingressIP.Status.AllocatedIP, chainName)
			} else {
				klog.Warningf("%q annotation is missing on %q", kubeProxyIPTableChainAnnotation, key)
			}
		} else if ingressIP.Spec.Target == submarinerv1.HeadlessServicePod {
			podIP := ingressIP.GetAnnotations()[headlessSvcPodIP]
			if podIP != "" {
				if err := c.iptIface.RemoveIngressRulesForHeadlessSvcPod(ingressIP.Status.AllocatedIP, podIP); err != nil {
					return err
				}

				return c.iptIface.RemoveEgressRulesForHeadlessSVCPods(key, podIP, ingressIP.Status.AllocatedIP, globalNetIPTableMark)
			} else {
				klog.Warningf("%q annotation is missing on pod %q", headlessSvcPodIP, key)
			}
		}

		return nil
	}, ingressIP.Status.AllocatedIP)
}

func getIngressIPName(obj runtime.Object) string {
	switch typedObj := obj.(type) {
	case *corev1.Service:
		return fmt.Sprintf("svc-%.59s", typedObj.Name)
	case *corev1.Pod:
		return fmt.Sprintf("pod-%.59s", typedObj.Name)
	}

	return ""
}

func (c *globalIngressIPController) getPod(name, namespace string) (*corev1.Pod, bool, error) {
	obj, err := c.pods.Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}

	if err != nil {
		klog.Errorf("Error retrieving Pod %s/%s: %v", namespace, name, err)
		return nil, false, err
	}

	pod := &corev1.Pod{}
	err = c.config.Scheme.Convert(obj, pod, nil)
	if err != nil {
		klog.Errorf("Error converting %#v to Pod: %v", obj, err)
		return nil, false, err
	}

	return pod, true, nil
}

func (c *globalIngressIPController) deleteIngressObject(obj *unstructured.Unstructured,
	client dynamic.NamespaceableResourceInterface) error {
	gip := &submarinerv1.GlobalIngressIP{}
	_ = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, gip)

	c.onDelete(gip, 0)
	err := client.Namespace(gip.Namespace).Delete(context.TODO(), gip.Name, metav1.DeleteOptions{})
	if err != nil {
		klog.Errorf("Error deleting the IngressIP %q/%q object: err: %v", gip.Namespace, gip.Name, err)
		return err
	}

	return nil
}
