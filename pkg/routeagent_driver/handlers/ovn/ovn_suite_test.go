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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	fakesubm "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakenetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
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
	submClient *fakesubm.Clientset
	netLink    *fakenetlink.NetLink
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.submClient = fakesubm.NewSimpleClientset()

		t.netLink = fakenetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
	})

	return t
}
