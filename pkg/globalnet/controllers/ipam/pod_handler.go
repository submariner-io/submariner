/*
© 2021 Red Hat, Inc. and others

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
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func (i *Controller) onPodUpdated(oldObj, newObj *unstructured.Unstructured) bool {
	oldPodIP, _, _ := unstructured.NestedString(oldObj.Object, "status", "podIP")
	newPodIP, _, _ := unstructured.NestedString(newObj.Object, "status", "podIP")

	// When a pod is being terminated, sometimes we get pod update event with podIP removed.
	if oldPodIP != "" && newPodIP == "" {
		klog.V(log.DEBUG).Infof("Pod %q with ip %s is being terminated", newObj.GetName(), oldPodIP)
		i.processRemovedPod(i.fromUnstructured(oldObj, &k8sv1.Pod{}).(*k8sv1.Pod))

		return true
	}

	return oldPodIP == newPodIP && i.areGlobalIPsEquivalent(oldObj, newObj)
}

func (i *Controller) processPod(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	pod := from.(*k8sv1.Pod)

	if pod.Status.PodIP == "" {
		return nil, false
	}

	if pod.Spec.HostNetwork {
		klog.V(log.DEBUG).Infof("Ignoring pod %q on host %q as it uses hostNetworking", pod.Name, pod.Status.PodIP)
		return nil, false
	}

	if op == syncer.Delete {
		i.processRemovedPod(pod)
		return nil, false
	}

	return i.updatePod(pod)
}

func (i *Controller) processRemovedPod(pod *k8sv1.Pod) {
	globalIP := pod.Annotations[SubmarinerIPAMGlobalIP]
	if globalIP != "" && pod.Status.PodIP != "" {
		key, _ := cache.MetaNamespaceKeyFunc(pod)

		i.pool.Release(key)

		klog.V(log.DEBUG).Infof("Released globalIP %s for Pod %q", globalIP, key)

		err := i.syncPodRules(pod.Status.PodIP, globalIP, DeleteRules)
		if err != nil {
			klog.Errorf("Error while cleaning up Pod egress rules: %v", err)
		}
	}
}

func (i *Controller) updatePod(pod *k8sv1.Pod) (runtime.Object, bool) {
	return i.updateGlobalIP(pod, func(globalIP string) error {
		return i.syncPodRules(pod.Status.PodIP, globalIP, AddRules)
	})
}
