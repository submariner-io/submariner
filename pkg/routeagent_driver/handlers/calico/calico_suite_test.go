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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	calicoapi "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/submariner/pkg/event"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

type testDriver struct {
	*eventtesting.ControllerSupport
	handler   event.Handler
	k8sClient *fakek8s.Clientset
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.k8sClient = fakek8s.NewSimpleClientset()
	})

	return t
}

func (t *testDriver) Start(handler event.Handler) {
	t.handler = handler
	t.ControllerSupport.Start(handler)
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
	Expect(calicoapi.AddToScheme(scheme.Scheme)).To(Succeed())
})

func TestCalico(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Calico Suite")
}
