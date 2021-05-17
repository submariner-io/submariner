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
package framework

import (
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func (f *Framework) AwaitGatewayWithStatus(cluster framework.ClusterIndex, name string,
	status submarinerv1.HAStatus) *submarinerv1.Gateway {
	return toGateway(f.Framework.AwaitGatewayWithStatus(cluster, name, string(status)))
}

func (f *Framework) AwaitGatewaysWithStatus(cluster framework.ClusterIndex, status submarinerv1.HAStatus) []submarinerv1.Gateway {
	return toGateways(f.Framework.AwaitGatewaysWithStatus(cluster, string(status)))
}

func (f *Framework) AwaitGatewayFullyConnected(cluster framework.ClusterIndex, name string) *submarinerv1.Gateway {
	return toGateway(f.Framework.AwaitGatewayFullyConnected(cluster, name))
}

func (f *Framework) GetGatewaysWithHAStatus(cluster framework.ClusterIndex, status submarinerv1.HAStatus) []submarinerv1.Gateway {
	return toGateways(f.Framework.GetGatewaysWithHAStatus(cluster, string(status)))
}

func toGateway(from *unstructured.Unstructured) *submarinerv1.Gateway {
	to := &submarinerv1.Gateway{}
	Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(from.Object, to)).To(Succeed())

	return to
}

func toGateways(from []unstructured.Unstructured) []submarinerv1.Gateway {
	gateways := make([]submarinerv1.Gateway, len(from))
	for i := range from {
		gateways[i] = *toGateway(&from[i])
	}

	return gateways
}
