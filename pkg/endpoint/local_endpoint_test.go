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
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/types"
	v1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

const testNodeName = "this-node"

var _ = Describe("GetLocal", func() {
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

	It("should return a valid SubmarinerEndpoint object", func() {
		endpoint, err := endpoint.GetLocal(submSpec, client, false)

		Expect(err).ToNot(HaveOccurred())
		Expect(endpoint.Spec.ClusterID).To(Equal("east"))
		Expect(endpoint.Spec.CableName).To(HavePrefix("submariner-cable-east-"))
		Expect(endpoint.Spec.Hostname).NotTo(Equal(""))
		Expect(endpoint.Spec.PrivateIP).To(Equal(testPrivateIP))
		Expect(endpoint.Spec.Backend).To(Equal("backend"))
		Expect(endpoint.Spec.Subnets).To(Equal(subnets))
		Expect(endpoint.Spec.NATEnabled).To(Equal(false))
		Expect(endpoint.Spec.BackendConfig[testUDPPortLabel]).To(Equal(testUDPPort))
	})

	When("gateway node is not annotated with udp port", func() {
		It("should return the udp-port backend config of the cluster", func() {
			delete(node.Labels, backendConfigPrefix+testUDPPortLabel)
			client = fake.NewSimpleClientset(node)
			os.Setenv("CE_IPSEC_NATTPORT", testClusterUDPPort)

			endpoint, err := endpoint.GetLocal(submSpec, client, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(endpoint.Spec.BackendConfig[testUDPPortLabel]).To(Equal(testClusterUDPPort))
		})
	})

	When("gateway node is annotated with udp port", func() {
		It("should return the udp-port backend from the annotation", func() {
			os.Setenv("CE_IPSEC_NATTPORT", testClusterUDPPort)
			endpoint, err := endpoint.GetLocal(submSpec, client, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(endpoint.Spec.BackendConfig[testUDPPortLabel]).To(Equal(testUDPPort))
		})
	})

	When("no NAT discovery port label is set on the node", func() {
		It("should return a valid SubmarinerEndpoint object", func() {
			delete(node.Labels, testNATTPortLabel)
			_, err := endpoint.GetLocal(submSpec, client, false)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	When("gateway node is not annotated with public-ip", func() {
		It("should use empty public-ip in the endpoint object for air-gapped deployments", func() {
			endpoint, err := endpoint.GetLocal(submSpec, client, true)

			Expect(err).ToNot(HaveOccurred())
			Expect(endpoint.Spec.ClusterID).To(Equal("east"))
			Expect(endpoint.Spec.PrivateIP).To(Equal(testPrivateIP))
			Expect(endpoint.Spec.PublicIP).To(Equal(""))
		})
	})

	When("gateway node is annotated with public-ip", func() {
		It("should use the annotated public-ip for air-gapped deployments", func() {
			node.Labels[backendConfigPrefix+testPublicIPLabel] = testIPv4Label + testPublicIP
			client = fake.NewSimpleClientset(node)
			endpoint, err := endpoint.GetLocal(submSpec, client, true)

			Expect(err).ToNot(HaveOccurred())
			Expect(endpoint.Spec.PrivateIP).To(Equal(testPrivateIP))
			Expect(endpoint.Spec.PublicIP).To(Equal(testPublicIP))
		})
	})
})
