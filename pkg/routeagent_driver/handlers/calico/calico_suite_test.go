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

package calico_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	calicoapi "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	calicocs "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	calicocsfake "github.com/projectcalico/api/pkg/client/clientset_generated/clientset/fake"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/submariner/pkg/event"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/calico"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

type testDriver struct {
	*eventtesting.ControllerSupport
	handler      event.Handler
	k8sClient    *fakek8s.Clientset
	calicoClient *calicocsfake.Clientset
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.k8sClient = fakek8s.NewSimpleClientset()

		t.calicoClient = calicocsfake.NewSimpleClientset()
		fake.AddDeleteCollectionReactor(&t.calicoClient.Fake)

		calico.NewClient = func(_ *rest.Config) (calicocs.Interface, error) {
			return t.calicoClient, nil
		}
	})

	return t
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
	Expect(calicoapi.AddToScheme(scheme.Scheme)).To(Succeed())
})

func TestCalico(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Calico Suite")
}

func (t *testDriver) Start(handler event.Handler) {
	_, err := t.calicoClient.ProjectcalicoV3().IPPools().Create(context.Background(), &calicoapi.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: calico.DefaultV4IPPoolName,
		},
		Spec: calicoapi.IPPoolSpec{
			IPIPMode: calicoapi.IPIPModeNever,
		},
	}, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	t.handler = handler
	t.ControllerSupport.Start(handler)
}

func (t *testDriver) getIPPoolCIDRs() []string {
	list, err := t.calicoClient.ProjectcalicoV3().IPPools().List(context.Background(), metav1.ListOptions{
		LabelSelector: calico.SubmarinerIPPool + "=true",
	})
	Expect(err).To(Succeed())

	cidrs := make([]string, len(list.Items))
	for i := range list.Items {
		cidrs[i] = list.Items[i].Spec.CIDR
	}

	return cidrs
}

func (t *testDriver) ensureNoIPPools(subnets ...string) {
	Consistently(func() []string {
		return t.getIPPoolCIDRs()
	}).ShouldNot(ContainElements(toAny(subnets)...))
}

func (t *testDriver) ensureIPPools(subnets ...string) {
	Consistently(func() []string {
		return t.getIPPoolCIDRs()
	}).Should(ContainElements(toAny(subnets)...))
}

func (t *testDriver) awaitIPPools(subnets ...string) {
	Eventually(func() []string {
		return t.getIPPoolCIDRs()
	}).Should(ContainElements(toAny(subnets)...))
}

func (t *testDriver) awaitNoIPPools(subnets ...string) {
	Eventually(func() []string {
		return t.getIPPoolCIDRs()
	}).ShouldNot(ContainElements(toAny(subnets)...))
}

func (t *testDriver) getDefaultIPPoolIPIPMode() string {
	p, err := t.calicoClient.ProjectcalicoV3().IPPools().Get(context.Background(), calico.DefaultV4IPPoolName, metav1.GetOptions{})
	Expect(err).To(Succeed())

	return string(p.Spec.IPIPMode)
}

func (t *testDriver) createSubmarinerGwLBService(annotations map[string]string) {
	_, err := t.k8sClient.CoreV1().Services(eventtesting.Namespace).Create(context.Background(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        calico.GwLBSvcName,
			Annotations: annotations,
		},
	}, metav1.CreateOptions{})
	Expect(err).To(Succeed())
}

func toAny(s []string) []any {
	ia := make([]any, len(s))
	for i := range s {
		ia[i] = s[i]
	}

	return ia
}
