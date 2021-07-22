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
	"fmt"

	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func startIngressPodController(svc *corev1.Service, config syncer.ResourceSyncerConfig) (*ingressPodController, error) {
	var err error

	_, gvr, err := util.ToUnstructuredResource(&submarinerv1.GlobalIngressIP{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	controller := &ingressPodController{
		baseSyncerController: newBaseSyncerController(),
		svcName:              svc.Name,
		namespace:            svc.Namespace,
		ingressIPMap:         stringset.NewSynchronized(),
	}

	labelSelector := labels.Set(svc.Spec.Selector).AsSelector().String()

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "Ingress Pod syncer",
		ResourceType:        &corev1.Pod{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     svc.Namespace,
		RestMapper:          config.RestMapper,
		Federator:           federate.NewCreateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll),
		Scheme:              config.Scheme,
		Transform:           controller.process,
		SourceLabelSelector: labelSelector,
		ResourcesEquivalent: arePodsEqual,
	})

	if err != nil {
		return nil, err
	}

	if err := controller.Start(); err != nil {
		return nil, err
	}

	ingressIPs := config.SourceClient.Resource(*gvr).Namespace(corev1.NamespaceAll)
	controller.reconcile(ingressIPs, func(obj *unstructured.Unstructured) runtime.Object {
		podName, exists, _ := unstructured.NestedString(obj.Object, "spec", "podRef", "name")
		if exists {
			return &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: obj.GetNamespace(),
				},
			}
		}

		return nil
	})

	klog.Infof("Created Pod controller for (%s/%s) with selector %q", svc.Namespace, svc.Name, labelSelector)

	return controller, nil
}

func (c *ingressPodController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	pod := from.(*corev1.Pod)
	key, _ := cache.MetaNamespaceKeyFunc(pod)

	ingressIP := &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("pod-%.59s", pod.Name),
			Namespace: pod.Namespace,
			Labels: map[string]string{
				ServiceRefLabel: c.svcName,
			},
		},
	}

	if op == syncer.Delete {
		c.ingressIPMap.Remove(ingressIP.Name)
		klog.Infof("ingress Pod %s for service %s deleted", key, c.svcName)

		return ingressIP, false
	}

	// TODO: handle phase and podIP changes?
	if c.ingressIPMap.Contains(ingressIP.Name) || pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
		// Avoid assigning ingressIPs to pods that are not ready with an endpoint IP
		return nil, false
	}

	klog.Infof("%q ingress Pod %s for service %s", op, key, c.svcName)

	ingressIP.ObjectMeta.Annotations = map[string]string{
		headlessSvcPodIP: pod.Status.PodIP,
	}

	ingressIP.Spec = submarinerv1.GlobalIngressIPSpec{
		Target:     submarinerv1.HeadlessServicePod,
		ServiceRef: &corev1.LocalObjectReference{Name: c.svcName},
		PodRef:     &corev1.LocalObjectReference{Name: pod.Name},
	}

	c.ingressIPMap.Add(ingressIP.Name)

	return ingressIP, false
}

func arePodsEqual(obj1, obj2 *unstructured.Unstructured) bool {
	phase1, _, _ := unstructured.NestedString(obj1.Object, "status", "phase")
	phase2, _, _ := unstructured.NestedString(obj2.Object, "status", "phase")
	podIP1, _, _ := unstructured.NestedString(obj1.Object, "status", "podIP")
	podIP2, _, _ := unstructured.NestedString(obj2.Object, "status", "podIP")

	return phase1 == phase2 && podIP1 == podIP2
}
