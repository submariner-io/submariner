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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/submariner/pkg/ipset"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

func startEgressPodWatcher(name, namespace string, namedIPSet ipset.Named, config *watcher.Config,
	podSelector *metav1.LabelSelector,
) (*egressPodWatcher, error) {
	pw := &egressPodWatcher{
		stopCh:     make(chan struct{}),
		namedIPSet: namedIPSet,
	}

	sel, err := metav1.LabelSelectorAsSelector(podSelector)
	if err != nil {
		return nil, errors.Wrap(err, "error getting label selector")
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
					OnDeleteFunc: pw.onDelete,
				},
				ResourcesEquivalent: pw.arePodsEquivalent,
				SourceNamespace:     namespace,
				SourceLabelSelector: labelSelector,
			},
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "error creating resource watcher")
	}

	err = w.Start(pw.stopCh)
	if err != nil {
		return nil, errors.Wrap(err, "error starting resource watcher")
	}

	return pw, nil
}

func (w *egressPodWatcher) arePodsEquivalent(oldObj, newObj *unstructured.Unstructured) bool {
	oldPodIP, _, _ := unstructured.NestedString(oldObj.Object, "status", "podIP")
	newPodIP, _, _ := unstructured.NestedString(newObj.Object, "status", "podIP")

	return oldPodIP == newPodIP
}

func (w *egressPodWatcher) onCreateOrUpdate(obj runtime.Object, numRequeues int) bool {
	pod := obj.(*corev1.Pod)
	key, _ := cache.MetaNamespaceKeyFunc(pod)

	if pod.Status.PodIP == "" {
		return false
	}

	klog.V(log.DEBUG).Infof("Pod %q with IP %s created/updated", key, pod.Status.PodIP)

	if err := w.namedIPSet.AddEntry(pod.Status.PodIP, true); err != nil {
		klog.Errorf("Error adding pod IP %q to IP set %q: %v", pod.Status.PodIP, w.ipSetName, err)
		return true
	}

	return false
}

func (w *egressPodWatcher) onDelete(obj runtime.Object, numRequeues int) bool {
	pod := obj.(*corev1.Pod)
	key, _ := cache.MetaNamespaceKeyFunc(pod)

	klog.V(log.DEBUG).Infof("Pod %q removed", key)

	if err := w.namedIPSet.DelEntry(pod.Status.PodIP); err != nil {
		klog.Errorf("Error deleting pod IP %q from IP set %q: %v", pod.Status.PodIP, w.ipSetName, err)
		return true
	}

	return false
}
