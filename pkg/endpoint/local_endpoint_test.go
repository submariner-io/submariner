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

package endpoint_test

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/types"
	v1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

const testNodeName = "this-node"

var _ = Describe("GetLocalSpec", func() {
	var submSpec *types.SubmarinerSpecification
	var client kubernetes.Interface
	testPrivateIP := endpoint.GetLocalIP()
	var node *v1.Node

	const (
		testIPv4Label       = "ipv4:"
		testPublicIP        = "4.3.2.1"
		testUDPPort         = "1111"
		testClusterUDPPort  = "2222"
		testUDPPortLabel    = "udp-port"
		testPublicIPLabel   = "public-ip"
		testNATTPortLabel   = "natt-discovery-port"
		backendConfigPrefix = "gateway.submariner.io/"
	)

	subnets := []string{"127.0.0.1/16"}

	BeforeEach(func() {
		submSpec = &types.SubmarinerSpecification{
			ClusterID:   "east",
			ClusterCidr: subnets,
			CableDriver: "backend",
		}

		node = &v1.Node{
			ObjectMeta: v1meta.ObjectMeta{
				Name: testNodeName,
				Labels: map[string]string{
					backendConfigPrefix + testNATTPortLabel: "1234",
					backendConfigPrefix + testUDPPortLabel:  testUDPPort,
				},
			},
		}

		client = fake.NewSimpleClientset(node)

		os.Setenv("NODE_NAME", testNodeName)
	})

	It("should return a valid EndpointSpec object", func() {
		spec, err := endpoint.GetLocalSpec(submSpec, client, false)

		Expect(err).ToNot(HaveOccurred())
		Expect(spec.ClusterID).To(Equal("east"))
		Expect(spec.CableName).To(HavePrefix("submariner-cable-east-"))
		Expect(spec.Hostname).NotTo(BeEmpty())
		Expect(spec.PrivateIP).To(Equal(testPrivateIP))
		Expect(spec.Backend).To(Equal("backend"))
		Expect(spec.Subnets).To(Equal(subnets))
		Expect(spec.NATEnabled).To(BeFalse())
		Expect(spec.BackendConfig[testUDPPortLabel]).To(Equal(testUDPPort))
	})

	When("gateway node is not annotated with udp port", func() {
		It("should return the udp-port backend config of the cluster", func() {
			delete(node.Labels, backendConfigPrefix+testUDPPortLabel)
			client = fake.NewSimpleClientset(node)
			os.Setenv("CE_IPSEC_NATTPORT", testClusterUDPPort)

			spec, err := endpoint.GetLocalSpec(submSpec, client, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(spec.BackendConfig[testUDPPortLabel]).To(Equal(testClusterUDPPort))
		})
	})

	When("gateway node is annotated with udp port", func() {
		It("should return the udp-port backend from the annotation", func() {
			os.Setenv("CE_IPSEC_NATTPORT", testClusterUDPPort)
			spec, err := endpoint.GetLocalSpec(submSpec, client, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(spec.BackendConfig[testUDPPortLabel]).To(Equal(testUDPPort))
		})
	})

	When("no NAT discovery port label is set on the node", func() {
		It("should return a valid EndpointSpec object", func() {
			delete(node.Labels, testNATTPortLabel)
			_, err := endpoint.GetLocalSpec(submSpec, client, false)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	When("gateway node is not annotated with public-ip", func() {
		It("should use empty public-ip in the endpoint object for air-gapped deployments", func() {
			spec, err := endpoint.GetLocalSpec(submSpec, client, true)

			Expect(err).ToNot(HaveOccurred())
			Expect(spec.ClusterID).To(Equal("east"))
			Expect(spec.PrivateIP).To(Equal(testPrivateIP))
			Expect(spec.PublicIP).To(Equal(""))
		})
	})

	When("gateway node is annotated with public-ip", func() {
		It("should use the annotated public-ip for air-gapped deployments", func() {
			node.Labels[backendConfigPrefix+testPublicIPLabel] = testIPv4Label + testPublicIP
			client = fake.NewSimpleClientset(node)
			spec, err := endpoint.GetLocalSpec(submSpec, client, true)

			Expect(err).ToNot(HaveOccurred())
			Expect(spec.PrivateIP).To(Equal(testPrivateIP))
			Expect(spec.PublicIP).To(Equal(testPublicIP))
		})
	})
})

var _ = Describe("Local", func() {
	var (
		spec      *submarinerv1.EndpointSpec
		local     *endpoint.Local
		dynClient *dynamicfake.FakeDynamicClient
	)

	BeforeEach(func() {
		spec = &submarinerv1.EndpointSpec{
			CableName: "submariner-cable-192-68-1-2",
			ClusterID: "east",
			Hostname:  "redsox",
			PrivateIP: "192.68.1.2",
			PublicIP:  "1.2.3.4",
			Subnets:   []string{"100.0.0.0/16", "10.0.0.0/14"},
			Backend:   "ipsec",
		}

		dynClient = dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	})

	JustBeforeEach(func() {
		local = endpoint.NewLocal(spec, dynClient, testNamespace)

		test.CreateResource(dynClient.Resource(submarinerv1.EndpointGVR).Namespace(testNamespace), local.Resource())
	})

	Specify("Spec should return the correct data", func() {
		Expect(*local.Spec()).To(Equal(*spec))
	})

	Specify("Update should successfully update the resource", func() {
		spec.PublicIP = "11.22.33.44"

		Expect(local.Update(context.Background(), func(existing *submarinerv1.EndpointSpec) {
			existing.PublicIP = spec.PublicIP
		})).To(Succeed())

		Expect(*local.Spec()).To(Equal(*spec))

		endpoint := test.GetResource(dynClient.Resource(submarinerv1.EndpointGVR).Namespace(testNamespace),
			&submarinerv1.Endpoint{
				ObjectMeta: v1meta.ObjectMeta{Name: local.Resource().Name},
			})
		Expect(endpoint.Spec).To(Equal(*spec))
	})
})
