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

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/submariner/pkg/ipset"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func startPodWatcher(name, namespace string, namedIPSet ipset.Named, config watcher.Config,
	podSelector *metav1.LabelSelector) (*podWatcher, error) {
	pw := &podWatcher{
		stopCh:     make(chan struct{}),
		namedIPSet: namedIPSet,
	}

	sel, err := metav1.LabelSelectorAsSelector(podSelector)
	if err != nil {
		return nil, err
	}

	labelSelector := sel.String()

	w, err := watcher.New(&watcher.Config{
		RestMapper: config.RestMapper,
		Client:     config.Client,
		Scheme:     config.Scheme,
		ResourceConfigs: []watcher.ResourceConfig{
			{
				Name:         fmt.Sprintf("Pod watcher %s", name),
				ResourceType: &corev1.Pod{},
				Handler: watcher.EventHandlerFuncs{
					OnCreateFunc: pw.onCreateOrUpdate,
					OnUpdateFunc: pw.onCreateOrUpdate,
					OnDeleteFunc: pw.onRemove,
				},
				ResourcesEquivalent: pw.arePodsEquivalent,
				SourceNamespace:     namespace,
				SourceLabelSelector: labelSelector,
			},
		},
	})

	if err != nil {
		return nil, err
	}

	err = w.Start(pw.stopCh)
	if err != nil {
		return nil, err
	}

	return pw, nil
}

func (w *podWatcher) arePodsEquivalent(oldObj, newObj *unstructured.Unstructured) bool {
	oldPodIP, _, _ := unstructured.NestedString(oldObj.Object, "status", "podIP")
	newPodIP, _, _ := unstructured.NestedString(newObj.Object, "status", "podIP")

	return oldPodIP == newPodIP
}

func (w *podWatcher) onCreateOrUpdate(obj runtime.Object, numRequeues int) bool {
	pod := obj.(*corev1.Pod)
	key, _ := cache.MetaNamespaceKeyFunc(pod)

	if pod.Status.PodIP == "" {
		return false
	}

	klog.V(log.DEBUG).Infof("Pod %q with IP %s created/updated", key, pod.Status.PodIP)

	if err := w.addIPSetEntry(pod.Status.PodIP); err != nil {
		klog.Errorf("Error adding ipAddress %q to the ipSet chain %q: %v", pod.Status.PodIP, w.ipSetName, err)
		return true
	}

	return false
}

func (w *podWatcher) onRemove(obj runtime.Object, numRequeues int) bool {
	pod := obj.(*corev1.Pod)
	key, _ := cache.MetaNamespaceKeyFunc(pod)

	klog.V(log.DEBUG).Infof("Pod %q removed", key)

	if err := w.deleteIPSetEntry(pod.Status.PodIP); err != nil {
		klog.Errorf("Error deleting ipAddress %q from the ipSet chain %q: %v", pod.Status.PodIP, w.ipSetName, err)
		return true
	}

	return false
}

func (w *podWatcher) addIPSetEntry(ipAddress string) error {
	err := w.namedIPSet.AddEntry(ipAddress, true)
	if err != nil {
		return err
	}

	return nil
}

func (w *podWatcher) deleteIPSetEntry(ipAddress string) error {
	return w.namedIPSet.DelEntry(ipAddress)
}
