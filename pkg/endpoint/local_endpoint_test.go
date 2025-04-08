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
	"errors"
	"net"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/endpoint"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
	v1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
)

const testNodeName = "this-node"

var _ = Describe("GetLocalSpec", func() {
	var (
		submSpec *types.SubmarinerSpecification
		client   kubernetes.Interface
		node     *v1.Node
	)

	testPrivateIP := endpoint.GetLocalIP(k8snet.IPv4)

	const (
		testIPv4Label       = "ipv4:"
		testPublicIP        = "4.3.2.1"
		testUDPPort         = "1111"
		testClusterUDPPort  = "2222"
		testUDPPortLabel    = "udp-port"
		testPublicIPLabel   = "public-ip"
		testNATTPortLabel   = "natt-discovery-port"
		backendConfigPrefix = "gateway.submariner.io/"
		cniInterfaceIPv4    = "127.0.0.1"
		ipv4CIDR            = cniInterfaceIPv4 + "/16"
		cniInterfaceIPv6    = "2001:0:0:1234::"
		ipv6CIDR            = cniInterfaceIPv6 + "/64"
	)

	BeforeEach(func() {
		submSpec = &types.SubmarinerSpecification{
			ClusterID:   "east",
			ClusterCidr: []string{ipv4CIDR},
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

		os.Setenv("NODE_NAME", testNodeName)
		os.Setenv("CE_IPSEC_NATTPORT", testClusterUDPPort)

		cni.HostInterfaces = func() ([]cni.HostInterface, error) {
			return []cni.HostInterface{
				{
					Name: "veth0",
					Addr: ipv4CIDR,
				},
				{
					Name: "veth1",
					Addr: ipv6CIDR,
				},
			}, nil
		}

		netLink := fakeNetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return netLink
		}

		Expect(netLink.RouteAdd(&netlink.Route{
			LinkIndex: 1,
			Src:       net.ParseIP("1.2.3.4"),
			Gw:        net.ParseIP("1.2.0.0"),
		})).To(Succeed())

		Expect(netLink.RouteAdd(&netlink.Route{
			LinkIndex: 2,
			Src:       net.ParseIP("2001:0:0:4321::"),
			Gw:        net.ParseIP("2001:0:0:0::"),
		})).To(Succeed())
	})

	JustBeforeEach(func() {
		client = fake.NewClientset(node)
	})

	It("should return a valid EndpointSpec object", func() {
		spec, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, false)

		Expect(err).ToNot(HaveOccurred())
		Expect(spec.ClusterID).To(Equal("east"))
		Expect(spec.CableName).To(HavePrefix("submariner-cable-east-"))
		Expect(spec.Hostname).NotTo(BeEmpty())
		Expect(spec.GetPrivateIP(k8snet.IPv4)).To(Equal(testPrivateIP))
		Expect(spec.Backend).To(Equal("backend"))
		Expect(spec.Subnets).To(Equal(submSpec.ClusterCidr))
		Expect(spec.NATEnabled).To(BeFalse())
		Expect(spec.BackendConfig[testUDPPortLabel]).To(Equal(testUDPPort))
		Expect(spec.HealthCheckIPs).To(BeEmpty())
	})

	When("the gateway node is not annotated with udp port", func() {
		BeforeEach(func() {
			delete(node.Labels, backendConfigPrefix+testUDPPortLabel)
		})

		It("should return the udp-port backend config of the cluster", func() {
			spec, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(spec.BackendConfig[testUDPPortLabel]).To(Equal(testClusterUDPPort))
		})
	})

	When("no NAT discovery port label is set on the node", func() {
		BeforeEach(func() {
			delete(node.Labels, testNATTPortLabel)
		})

		It("should return a valid EndpointSpec object", func() {
			_, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, false)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	When("the gateway node is not annotated with public-ip", func() {
		It("should use empty public-ip in the endpoint object for air-gapped deployments", func() {
			spec, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, true)

			Expect(err).ToNot(HaveOccurred())
			Expect(spec.ClusterID).To(Equal("east"))
			Expect(spec.PrivateIPs).To(Equal([]string{testPrivateIP}))
			Expect(spec.PublicIPs).To(BeEmpty())
		})
	})

	When("the gateway node is annotated with public-ip", func() {
		BeforeEach(func() {
			node.Labels[backendConfigPrefix+testPublicIPLabel] = testIPv4Label + testPublicIP
		})

		It("should use the annotated public-ip for air-gapped deployments", func() {
			spec, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, true)

			Expect(err).ToNot(HaveOccurred())
			Expect(spec.PrivateIPs).To(Equal([]string{testPrivateIP}))
			Expect(spec.PublicIPs).To(Equal([]string{testPublicIP}))
		})
	})

	When("health check is enabled", func() {
		BeforeEach(func() {
			submSpec.HealthCheckEnabled = true
		})

		It("should set the HealthCheckIP", func() {
			spec, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, true)
			Expect(err).ToNot(HaveOccurred())
			Expect(spec.HealthCheckIPs).To(Equal([]string{cniInterfaceIPv4}))
		})

		Context("with dual-stack", func() {
			BeforeEach(func() {
				submSpec.ClusterCidr = append(submSpec.ClusterCidr, ipv6CIDR)
			})

			It("should set IPv4 and IPv6 HealthCheckIPs", func() {
				spec, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, true)
				Expect(err).ToNot(HaveOccurred())

				Expect(spec.HealthCheckIPs).To(ContainElements(cniInterfaceIPv4, cniInterfaceIPv6))
				Expect(spec.HealthCheckIPs).To(HaveLen(2))
			})
		})

		Context("and globalnet is enabled", func() {
			BeforeEach(func() {
				submSpec.GlobalCidr = []string{"242.10.0.0/24"}
			})

			It("should not set the HealthCheckIP", func() {
				spec, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, true)
				Expect(err).ToNot(HaveOccurred())
				Expect(spec.HealthCheckIPs).To(BeEmpty())
			})
		})

		Context("and CNI discovery fails", func() {
			BeforeEach(func() {
				cni.HostInterfaces = func() ([]cni.HostInterface, error) {
					return nil, errors.New("mock error")
				}
			})

			It("should return an error", func() {
				_, err := endpoint.GetLocalSpec(context.TODO(), submSpec, client, true)
				Expect(err).To(HaveOccurred())
			})
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
			CableName:     "submariner-cable-192-68-1-2",
			ClusterID:     "east",
			Hostname:      "redsox",
			PrivateIPs:    []string{"192.68.1.2"},
			PublicIPs:     []string{"1.2.3.4"},
			Subnets:       []string{"100.0.0.0/16", "10.0.0.0/14"},
			Backend:       "ipsec",
			BackendConfig: map[string]string{"foo": "bar"},
		}

		dynClient = dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	})

	JustBeforeEach(func() {
		local = endpoint.NewLocal(spec, dynClient, testNamespace)
	})

	verifyResource := func() {
		endpoint := test.GetResource(dynClient.Resource(submarinerv1.EndpointGVR).Namespace(testNamespace),
			&submarinerv1.Endpoint{
				ObjectMeta: v1meta.ObjectMeta{Name: local.Resource().Name},
			})
		Expect(endpoint.Spec).To(Equal(*spec))
	}

	Specify("Spec should return the correct data", func() {
		Expect(*local.Spec()).To(Equal(*spec))
	})

	Specify("Create followed by Update should create/update the resource in the datastore", func() {
		Expect(local.Create(context.TODO())).To(Succeed())

		verifyResource()

		spec.PublicIPs = []string{"11.22.33.44"}

		Expect(local.Update(context.Background(), func(existing *submarinerv1.EndpointSpec) {
			existing.PublicIPs = spec.PublicIPs
		})).To(Succeed())

		Expect(*local.Spec()).To(Equal(*spec))
		verifyResource()
	})

	Specify("Create with an existing resource in the datastore should update it", func() {
		r := local.Resource()
		r.Spec.PublicIPs = []string{"8.8.8.8"}
		test.CreateResource(dynClient.Resource(submarinerv1.EndpointGVR).Namespace(testNamespace), r)

		Expect(local.Create(context.TODO())).To(Succeed())

		verifyResource()
	})

	Specify("Update before creation should only update the cached Spec", func() {
		spec.PublicIPs = []string{"11.22.33.44"}

		Expect(local.Update(context.Background(), func(existing *submarinerv1.EndpointSpec) {
			existing.PublicIPs = spec.PublicIPs
		})).To(Succeed())

		Expect(*local.Spec()).To(Equal(*spec))
	})
})
