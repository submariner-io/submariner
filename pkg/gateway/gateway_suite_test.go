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

package gateway_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/fake"
	"github.com/submariner-io/submariner/pkg/cableengine/syncer"
	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
)

func init() {
	kzerolog.AddFlags(nil)
}

var fakeDriver *fake.Driver

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
	Expect(submarinerv1.AddToScheme(scheme.Scheme)).To(Succeed())

	cable.AddDriver(fake.DriverName, func(_ *types.SubmarinerEndpoint, _ *types.SubmarinerCluster) (cable.Driver, error) {
		return fakeDriver, nil
	})

	syncer.GatewayUpdateInterval = 50 * time.Millisecond
})

func TestGateway(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gateway Suite")
}
