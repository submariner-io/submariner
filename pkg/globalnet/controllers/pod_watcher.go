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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

func startPodWatcher(name, namespace string, config watcher.Config) (*podWatcher, error) {
	pw := &podWatcher{stopCh: make(chan struct{})}

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

	// TODO - perhaps we should store the oldPodIP and let the normal update handle it.
	// When a pod is being terminated, sometimes we get pod update event with podIP removed.
	if oldPodIP != "" && newPodIP == "" {
		klog.V(log.DEBUG).Infof("Pod %q with IP %s is being terminated", newObj.GetName(), oldPodIP)
		w.processRemoved(newObj.GetName(), oldPodIP)

		return true
	}

	return oldPodIP == newPodIP
}

func (w *podWatcher) onCreateOrUpdate(obj runtime.Object, numRequeues int) bool {
	pod := obj.(*corev1.Pod)

	if pod.Status.PodIP == "" {
		return false
	}

	klog.V(log.DEBUG).Infof("Pod %q with IP %s created/updated", pod.Name, pod.Status.PodIP)

	// TODO - IP table magic

	return false
}

func (w *podWatcher) onRemove(obj runtime.Object, numRequeues int) bool {
	pod := obj.(*corev1.Pod)

	klog.V(log.DEBUG).Infof("Pod %q removed", pod.Name)

	return w.processRemoved(pod.Name, pod.Status.PodIP)
}

func (w *podWatcher) processRemoved(name, podIP string) bool {
	// TODO - IP table magic
	return false
}
