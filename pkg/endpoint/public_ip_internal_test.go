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

package endpoint

import (
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/types"
	v1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("firstIPInString", func() {
	When("the content has an IPv4", func() {
		const testIP = "1.2.3.4"
		const jsonIP = "{\"ip\": \"" + testIP + "\"}"

		It("should return the IP", func() {
			ip, err := firstIPInString(k8snet.IPv4, jsonIP)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
		})
	})

	When("the content doesn't have an IPv4", func() {
		It("should result in error", func() {
			ip, err := firstIPInString(k8snet.IPv4, "no IPs here")
			Expect(err).To(HaveOccurred())
			Expect(ip).To(Equal(""))
		})
	})
	When("the content has an IPv6", func() {
		const testIP = "2a00:a041:f123:1400:1dd1:2a92:b926:8019"
		const jsonIP = "\"" + testIP + "\\\n" + "\""

		It("should return the IP", func() {
			ip, err := firstIPInString(k8snet.IPv6, jsonIP)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
		})
	})

	When("the content doesn't have an IPv6", func() {
		It("should result in error", func() {
			ip, err := firstIPInString(k8snet.IPv6, "{\"ip\": \"1.2.3.4\"")
			Expect(err).To(HaveOccurred())
			Expect(ip).To(Equal(""))
		})
	})
})

const (
	testServiceName = "my-loadbalancer"
	testNamespace   = "namespace"
)

var _ = Describe("public ip resolvers", func() {
	var submSpec *types.SubmarinerSpecification
	var backendConfig map[string]string

	const (
		publicIPConfig   = "public-ip"
		testIPDNS        = "4.3.2.1"
		testIP           = "1.2.3.4"
		testIPv6         = "2001:4860:4860::8765"
		testIPv6DNS      = "2001:db8::1234"
		testIPv6DNSSslip = "2001-db8--1234"
		dnsHost          = testIPDNS + ".nip.io"
		dnsHostv6        = testIPv6DNSSslip + ".sslip.io"
		ipv4PublicIP     = "ipv4:" + testIP
		ipv6PublicIP     = "ipv6=" + testIPv6
		lbPublicIP       = "lb:" + testServiceName
	)

	BeforeEach(func() {
		submSpec = &types.SubmarinerSpecification{
			Namespace: testNamespace,
		}

		backendConfig = map[string]string{}
	})

	When("a LoadBalancer with Ingress IP is specified", func() {
		It("should return the IPv4", func() {
			backendConfig[publicIPConfig] = lbPublicIP
			client := fake.NewClientset(serviceWithIngress(v1.LoadBalancerIngress{Hostname: "", IP: testIP}))
			ip, resolver, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
			Expect(resolver).To(Equal(lbPublicIP))
		})
		It("should return the IPv6", func() {
			backendConfig[publicIPConfig] = lbPublicIP
			client := fake.NewClientset(serviceWithIngress(v1.LoadBalancerIngress{Hostname: "", IP: testIPv6}))
			ip, resolver, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPv6))
			Expect(resolver).To(Equal(lbPublicIP))
		})
	})

	When("a LoadBalancer with Ingress hostname is specified", func() {
		It("should return the IPv4 address", func() {
			backendConfig[publicIPConfig] = lbPublicIP
			client := fake.NewClientset(serviceWithIngress(v1.LoadBalancerIngress{
				Hostname: dnsHost,
				IP:       "",
			}))
			ip, resolver, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPDNS))
			Expect(resolver).To(Equal(lbPublicIP))
		})
		It("should return the IPv6 address", func() {
			backendConfig[publicIPConfig] = lbPublicIP
			client := fake.NewClientset(serviceWithIngress(v1.LoadBalancerIngress{
				Hostname: dnsHostv6,
				IP:       "",
			}))
			ip, resolver, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPv6DNS))
			Expect(resolver).To(Equal(lbPublicIP))
		})
	})

	When("a LoadBalancer with no ingress is specified", func() {
		It("should return error with IPv4", func() {
			loadBalancerRetryConfig.Cap = 1 * time.Second
			loadBalancerRetryConfig.Duration = 50 * time.Millisecond
			loadBalancerRetryConfig.Steps = 1
			backendConfig[publicIPConfig] = lbPublicIP
			client := fake.NewClientset(serviceWithIngress())
			_, _, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, false)
			Expect(err).To(HaveOccurred())
		})
		It("should return error with IPv6", func() {
			loadBalancerRetryConfig.Cap = 1 * time.Second
			loadBalancerRetryConfig.Duration = 50 * time.Millisecond
			loadBalancerRetryConfig.Steps = 1
			backendConfig[publicIPConfig] = lbPublicIP
			client := fake.NewClientset(serviceWithIngress())
			_, _, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, false)
			Expect(err).To(HaveOccurred())
		})
	})

	When("an IP entry specified", func() {
		It("should return the IPv4 address", func() {
			backendConfig[publicIPConfig] = ipv4PublicIP
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
			Expect(resolver).To(Equal(ipv4PublicIP))
		})

		It("should return the IPv6 address", func() {
			backendConfig[publicIPConfig] = ipv6PublicIP
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPv6))
			Expect(resolver).To(Equal(ipv6PublicIP))
		})
	})

	When("an IP entry specified in air-gapped deployment", func() {
		It("should return the IPv4 and not an empty value", func() {
			backendConfig[publicIPConfig] = ipv4PublicIP
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, true)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
			Expect(resolver).To(Equal(ipv4PublicIP))
		})
		It("should return the IPv6 and not an empty value", func() {
			backendConfig[publicIPConfig] = ipv6PublicIP
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, true)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPv6))
			Expect(resolver).To(Equal(ipv6PublicIP))
		})
	})

	When("a DNS entry specified", func() {
		It("should return the IPv4 address", func() {
			backendConfig[publicIPConfig] = "dns:" + dnsHost
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPDNS))
			Expect(resolver).To(Equal(backendConfig[publicIPConfig]))
		})
		It("should return the IPv6 address", func() {
			backendConfig[publicIPConfig] = "dns:" + dnsHostv6
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPv6DNS))
			Expect(resolver).To(Equal(backendConfig[publicIPConfig]))
		})
	})

	When("an API entry specified", func() {
		It("should return some IPv4", func() {
			backendConfig[publicIPConfig] = "api:4.icanhazip.com/"
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			parseIP := net.ParseIP(ip)
			Expect(parseIP).NotTo(BeNil())
			Expect(parseIP.To4()).NotTo(BeNil())
			Expect(resolver).To(Equal(backendConfig[publicIPConfig]))
		})
		// temporary disable this test because of GHA IPv6 limitation
		/*
			It("should return some IPv6", func() {
				backendConfig[publicIPConfig] = "api:api64.ipify.org/"
				client := fake.NewClientset()
				ip, resolver, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, false)
				Expect(err).ToNot(HaveOccurred())
				parseIP := net.ParseIP(ip)
				Expect(parseIP).NotTo(BeNil())
				Expect(parseIP.To16()).NotTo(BeNil())
				Expect(resolver).To(Equal(backendConfig[publicIPConfig]))
			})
		*/
	})

	When("multiple entries are specified", func() {
		It("should return the first IPv4 working one", func() {
			backendConfig[publicIPConfig] = ipv4PublicIP + ",dns:" + dnsHost
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
			Expect(resolver).To(Equal(ipv4PublicIP))
		})
		It("should return the first IPv6 working one", func() {
			backendConfig[publicIPConfig] = ipv6PublicIP + ",dns:" + dnsHostv6
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPv6))
			Expect(resolver).To(Equal(ipv6PublicIP))
		})
	})

	When("multiple entries are specified and the first one doesn't succeed", func() {
		It("should return the first IPv4 working one", func() {
			backendConfig[publicIPConfig] = "dns:thisdomaindoesntexistforsure.badbadbad," + ipv4PublicIP
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv4, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
			Expect(resolver).To(Equal(ipv4PublicIP))
		})
		It("should return the first IPv4 working one", func() {
			backendConfig[publicIPConfig] = "dns:thisdomaindoesntexistforsure.badbadbad," + ipv6PublicIP
			client := fake.NewClientset()
			ip, resolver, err := getPublicIP(k8snet.IPv6, submSpec, client, backendConfig, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPv6))
			Expect(resolver).To(Equal(ipv6PublicIP))
		})
	})
})

func serviceWithIngress(ingress ...v1.LoadBalancerIngress) *v1.Service {
	return &v1.Service{
		ObjectMeta: v1meta.ObjectMeta{
			Name:      testServiceName,
			Namespace: testNamespace,
		},
		Status: v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{
				Ingress: ingress,
			},
		},
	}
}
