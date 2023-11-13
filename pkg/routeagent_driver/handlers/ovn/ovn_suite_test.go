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

package ovn_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	fakesubm "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	"github.com/submariner-io/submariner/pkg/event"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakenetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"
)

func init() {
	kzerolog.AddFlags(nil)
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
})

func TestOvn(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ovn Suite")
}

type testDriver struct {
	*eventtesting.ControllerSupport
	submClient      *fakesubm.Clientset
	k8sClient       *fakek8s.Clientset
	netLink         *fakenetlink.NetLink
	transitSwitchIP string
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.transitSwitchIP = "192.0.2.0"
		t.submClient = fakesubm.NewSimpleClientset()
		t.k8sClient = fakek8s.NewSimpleClientset()

		t.netLink = fakenetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
	})

	return t
}

func (t *testDriver) Start(handler event.Handler) {
	t.createNode()
	t.ControllerSupport.Start(handler)
}

func (t *testDriver) createNode() {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	if t.transitSwitchIP != "" {
		data := map[string]string{"ipv4": t.transitSwitchIP + "/24"}
		bytes, err := json.Marshal(data)
		Expect(err).To(Succeed())

		node.Annotations = map[string]string{constants.OvnTransitSwitchIPAnnotation: string(bytes)}
	}

	_, err := t.k8sClient.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	os.Setenv("NODE_NAME", node.Name)
}
