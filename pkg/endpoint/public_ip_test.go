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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/types"
	v1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("firstIPv4InString", func() {
	When("the content has an IPv4", func() {
		const testIP = "1.2.3.4"
		const jsonIP = "{\"ip\": \"" + testIP + "\"}"

		It("should return the IP", func() {
			ip, err := firstIPv4InString(jsonIP)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
		})
	})

	When("the content doesn't have an IPv4", func() {
		It("should result in error", func() {
			ip, err := firstIPv4InString("no IPs here")
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
		publicIPConfig = "public-ip"
		testIPDNS      = "4.3.2.1"
		testIP         = "1.2.3.4"
	)

	BeforeEach(func() {
		submSpec = &types.SubmarinerSpecification{
			Namespace: testNamespace,
		}

		backendConfig = map[string]string{}
	})

	When("a LoadBalancer with Ingress IP is specified", func() {
		It("should return the IP", func() {
			backendConfig[publicIPConfig] = "lb:" + testServiceName
			client := fake.NewSimpleClientset(serviceWithIngress(v1.LoadBalancerIngress{Hostname: "", IP: testIP}))
			ip, err := getPublicIP(submSpec, client, backendConfig)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
		})
	})

	When("a LoadBalancer with Ingress hostname is specified", func() {
		It("should return the IP", func() {
			backendConfig[publicIPConfig] = "lb:" + testServiceName
			client := fake.NewSimpleClientset(serviceWithIngress(v1.LoadBalancerIngress{Hostname: testIPDNS + ".nip.io",
				IP: ""}))
			ip, err := getPublicIP(submSpec, client, backendConfig)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPDNS))
		})
	})

	When("a LoadBalancer with no ingress is specified", func() {
		It("should return error", func() {
			loadBalancerRetryConfig.Cap = 1 * time.Second
			backendConfig[publicIPConfig] = "lb:" + testServiceName
			client := fake.NewSimpleClientset(serviceWithIngress())
			_, err := getPublicIP(submSpec, client, backendConfig)
			Expect(err).To(HaveOccurred())
		})
	})

	When("an IPv4 entry specified", func() {
		It("should return the IP", func() {
			backendConfig[publicIPConfig] = "ipv4:" + testIP
			client := fake.NewSimpleClientset()
			ip, err := getPublicIP(submSpec, client, backendConfig)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
		})
	})

	When("a DNS entry specified", func() {
		It("should return the IP", func() {
			backendConfig[publicIPConfig] = "dns:" + testIPDNS + ".nip.io"
			client := fake.NewSimpleClientset()
			ip, err := getPublicIP(submSpec, client, backendConfig)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIPDNS))
		})
	})

	When("an API entry specified", func() {
		It("should return some IP", func() {
			backendConfig[publicIPConfig] = "api:api.ipify.org"
			client := fake.NewSimpleClientset()
			ip, err := getPublicIP(submSpec, client, backendConfig)
			Expect(err).ToNot(HaveOccurred())
			Expect(net.ParseIP(ip)).NotTo(BeNil())
		})
	})

	When("multiple entries are specified", func() {
		It("should return the first working one", func() {
			backendConfig[publicIPConfig] = "ipv4:" + testIP + ",dns:" + testIPDNS + ".nip.io"
			client := fake.NewSimpleClientset()
			ip, err := getPublicIP(submSpec, client, backendConfig)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
		})
	})

	When("multiple entries are specified and the first one doesn't succeed", func() {
		It("should return the first working one", func() {
			backendConfig[publicIPConfig] = "dns:thisdomaindoesntexistforsure.badbadbad,ipv4:" + testIP
			client := fake.NewSimpleClientset()
			ip, err := getPublicIP(submSpec, client, backendConfig)
			Expect(err).ToNot(HaveOccurred())
			Expect(ip).To(Equal(testIP))
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
