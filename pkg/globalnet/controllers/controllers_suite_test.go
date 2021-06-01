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
package controllers_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/iptables"
	fakeIPT "github.com/submariner-io/submariner/pkg/iptables/fake"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	fakeDynClient "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	namespace          = "submariner"
	localCIDR          = "169.254.1.0/24"
	globalEgressIPName = "east-region"
)

func init() {
	klog.InitFlags(nil)

	_ = submarinerv1.AddToScheme(scheme.Scheme)
	_ = mcsv1a1.AddToScheme(scheme.Scheme)
}

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Globalnet Controllers Suite")
}

type testDriverBase struct {
	controller             controllers.Interface
	restMapper             meta.RESTMapper
	dynClient              dynamic.Interface
	scheme                 *runtime.Scheme
	ipt                    *fakeIPT.IPTables
	globalCIDR             string
	globalEgressIPs        dynamic.ResourceInterface
	clusterGlobalEgressIPs dynamic.ResourceInterface
	pods                   dynamic.NamespaceableResourceInterface
}

func newTestDriverBase() *testDriverBase {
	t := &testDriverBase{
		restMapper: test.GetRESTMapperFor(&submarinerv1.Endpoint{}, &corev1.Service{}, &corev1.Node{}, &corev1.Pod{},
			&submarinerv1.GlobalEgressIP{}, &submarinerv1.ClusterGlobalEgressIP{}, &mcsv1a1.ServiceExport{}),
		scheme:     runtime.NewScheme(),
		ipt:        fakeIPT.New(),
		globalCIDR: localCIDR,
	}

	Expect(mcsv1a1.AddToScheme(t.scheme)).To(Succeed())
	Expect(submarinerv1.AddToScheme(t.scheme)).To(Succeed())
	Expect(corev1.AddToScheme(t.scheme)).To(Succeed())

	t.dynClient = fakeDynClient.NewSimpleDynamicClient(t.scheme)

	t.globalEgressIPs = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.restMapper, &submarinerv1.GlobalEgressIP{})).
		Namespace(namespace)

	t.clusterGlobalEgressIPs = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.restMapper, &submarinerv1.ClusterGlobalEgressIP{}))

	t.pods = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.restMapper, &corev1.Pod{}))

	iptables.NewFunc = func() (iptables.Interface, error) {
		return t.ipt, nil
	}

	return t
}

func (t *testDriverBase) afterEach() {
	t.controller.Stop()

	iptables.NewFunc = nil
}

func (t *testDriverBase) createGlobalEgressIP(egressIP *submarinerv1.GlobalEgressIP) {
	test.CreateResource(t.globalEgressIPs, egressIP)
}

func (t *testDriverBase) createClusterGlobalEgressIP(egressIP *submarinerv1.ClusterGlobalEgressIP) {
	test.CreateResource(t.clusterGlobalEgressIPs, egressIP)
}

// nolint unparam - `name` always receives `globalEgressIPName`
func (t *testDriverBase) awaitGlobalEgressIPStatusAllocated(name string, expNumIPS int) {
	awaitStatusAllocated(t.globalEgressIPs, name, t.globalCIDR, expNumIPS)
}

func (t *testDriverBase) awaitClusterGlobalEgressIPStatusAllocated(name string, expNumIPS int) {
	awaitStatusAllocated(t.clusterGlobalEgressIPs, name, t.globalCIDR, expNumIPS)
}

func (t *testDriverBase) createPod(p *corev1.Pod) *corev1.Pod {
	return fromUnstructured(test.CreateResource(t.pods.Namespace(p.Namespace), p), &corev1.Pod{}).(*corev1.Pod)
}

func (t *testDriverBase) deletePod(p *corev1.Pod) {
	Expect(t.pods.Namespace(p.Namespace).Delete(context.TODO(), p.Name, metav1.DeleteOptions{})).To(Succeed())
}

func getGlobalEgressIPStatus(client dynamic.ResourceInterface, name string) *submarinerv1.GlobalEgressIPStatus {
	obj, err := client.Get(context.TODO(), name, metav1.GetOptions{})
	Expect(err).To(Succeed())

	statusObj, ok, err := unstructured.NestedMap(obj.Object, "status")
	Expect(err).To(Succeed())

	if !ok {
		return nil
	}

	status := &submarinerv1.GlobalEgressIPStatus{}
	Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(statusObj, status)).To(Succeed())

	return status
}

func awaitNoAllocatedIPs(client dynamic.ResourceInterface, name string) {
	Consistently(func() int {
		status := getGlobalEgressIPStatus(client, name)
		if status == nil {
			return 0
		}

		return len(status.AllocatedIPs)
	}, 200*time.Millisecond).Should(Equal(0))
}

// nolint unparam - `atIndex` always receives `0` - remove once a caller passes non-zero
func awaitGlobalEgressIPStatus(client dynamic.ResourceInterface, name, globalCIDR string, expNumIPS int, atIndex int,
	expCond ...metav1.Condition) {
	awaitStatusConditions(client, name, atIndex, expCond...)

	status := getGlobalEgressIPStatus(client, name)

	Expect(status.AllocatedIPs).To(HaveLen(expNumIPS))

	for _, ip := range status.AllocatedIPs {
		Expect(isValidIPForCIDR(globalCIDR, ip)).To(BeTrue(), "Allocated global IP %q is not valid for CIDR %q", ip, globalCIDR)
	}
}

func awaitStatusAllocated(client dynamic.ResourceInterface, name, globalCIDR string, expNumIPS int) {
	awaitGlobalEgressIPStatus(client, name, globalCIDR, expNumIPS, 0, metav1.Condition{
		Type:   string(submarinerv1.GlobalEgressIPAllocated),
		Status: metav1.ConditionTrue,
	})
}

func awaitStatusConditions(client dynamic.ResourceInterface, name string, atIndex int, expCond ...metav1.Condition) {
	var conditions []metav1.Condition
	var notFound error

	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		obj, err := client.Get(context.TODO(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			notFound = err
			return false, nil
		}

		notFound = nil

		if err != nil {
			return false, err
		}

		slice, ok, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if !ok || err != nil {
			return false, err
		}

		conditions = make([]metav1.Condition, len(slice))
		for i := range slice {
			c := &metav1.Condition{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(slice[i].(map[string]interface{}), c)
			if err != nil {
				return false, err
			}

			conditions[i] = *c
		}

		if (atIndex + len(expCond)) != len(conditions) {
			return false, nil
		}

		return true, nil
	})

	if notFound != nil {
		Fail(fmt.Sprintf("%#v", notFound))
		return
	}

	if err == wait.ErrWaitTimeout {
		if conditions == nil {
			Fail("Status conditions not found")
		}

		Fail(format.Message(conditions, fmt.Sprintf("to contain at index %d", atIndex), expCond))

		return
	}

	Expect(err).To(Succeed())

	j := atIndex

	for _, exp := range expCond {
		actual := conditions[j]
		j++

		Expect(actual.Type).To(Equal(exp.Type))
		Expect(actual.Status).To(Equal(exp.Status))
		Expect(actual.LastTransitionTime).To(Not(BeNil()))
		Expect(actual.Message).To(Not(BeEmpty()))

		if exp.Reason != "" {
			Expect(actual.Reason).To(Equal(exp.Reason))
		} else {
			Expect(actual.Reason).To(Not(BeEmpty()))
		}
	}
}

// nolint unparam - `name` always receives `globalEgressIPName` (`"east-region")
func newGlobalEgressIP(name string, numberOfIPs *int, podSelector *metav1.LabelSelector) *submarinerv1.GlobalEgressIP {
	return &submarinerv1.GlobalEgressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: submarinerv1.GlobalEgressIPSpec{
			NumberOfIPs: numberOfIPs,
			PodSelector: podSelector,
		},
	}
}

func newClusterGlobalEgressIP(name string, numIPs int) *submarinerv1.ClusterGlobalEgressIP {
	var numberOfIPs *int
	if numIPs >= 0 {
		numberOfIPs = &numIPs
	}

	return &submarinerv1.ClusterGlobalEgressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: submarinerv1.ClusterGlobalEgressIPSpec{
			NumberOfIPs: numberOfIPs,
		},
	}
}

func newPod(namespace string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: namespace,
		},
		Status: corev1.PodStatus{
			PodIP: "1.2.3.4",
		},
	}
}

func isValidIPForCIDR(cidr, ip string) bool {
	_, ipnet, err := net.ParseCIDR(cidr)
	Expect(err).NotTo(HaveOccurred())

	return ipnet.Contains(net.ParseIP(ip))
}

func fromUnstructured(from *unstructured.Unstructured, to runtime.Object) runtime.Object {
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(from.Object, to)
	Expect(err).To(Succeed())

	return to
}
