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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/types"
	v1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8snet "k8s.io/utils/net"
)

const (
	testIPv4    = "1.2.3.4"
	testIPv6    = "2001:4860:4860::8765"
	testIPv4DNS = "4.3.2.1"
	testIPv6DNS = "2001:db8::1234"
	dnsHostv4   = testIPv4DNS + ".nip.io"
	dnsHostv6   = testIPv6DNS + ".sslip.io"
)

var _ = Describe("Public IP resolvers", func() {
	Describe("IP Family", testIPFamilyResolver)
	Describe("DNS", testDNSResolver)
	Describe("API", testAPIResolver)
	Describe("LoadBalancer", testLoadBalancerResolver)
	Describe("Air gapped", testResolverInAirGapped)
	Describe("Multiple", testMultipleResolvers)
})

func testIPFamilyResolver() {
	t := newResolverTestDriver()

	testGetPublicIP := func(family k8snet.IPFamily, prefix, expectedIP string) {
		When(fmt.Sprintf("an IPv%s entry is specified", family), func() {
			It("should return the IP address", func() {
				backendConfig := map[string]string{submarinerv1.PublicIP: prefix + ":" + expectedIP}
				ip, resolver, err := endpoint.GetPublicIP(family, t.submSpec, fake.NewClientset(), backendConfig, false)

				Expect(err).NotTo(HaveOccurred())
				Expect(ip).To(Equal(expectedIP))
				Expect(resolver).To(Equal(backendConfig[submarinerv1.PublicIP]))
			})
		})
	}

	testGetPublicIP(k8snet.IPv4, submarinerv1.IPv4, testIPv4)
	testGetPublicIP(k8snet.IPv6, submarinerv1.IPv6, testIPv6)
}

func testDNSResolver() {
	t := newResolverTestDriver()

	testGetPublicIP := func(family k8snet.IPFamily, hostname, expectedIP string) {
		When(fmt.Sprintf("an IPv%s host name is specified", family), func() {
			It("should return the resolved IP address", func() {
				backendConfig := map[string]string{submarinerv1.PublicIP: submarinerv1.DNS + ":" + hostname}
				ip, resolver, err := endpoint.GetPublicIP(family, t.submSpec, fake.NewClientset(), backendConfig, false)

				Expect(err).NotTo(HaveOccurred())
				Expect(ip).To(Equal(expectedIP))
				Expect(resolver).To(Equal(backendConfig[submarinerv1.PublicIP]))
			})
		})
	}

	testGetPublicIP(k8snet.IPv4, dnsHostv4, testIPv4DNS)
	testGetPublicIP(k8snet.IPv6, dnsHostv6, testIPv6DNS)
}

func testAPIResolver() {
	t := newResolverTestDriver()

	BeforeEach(func() {
		t.setupHTTPServer(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "v4 %s v6 %s", testIPv4DNS, testIPv6DNS)
		})
	})

	testGetPublicIP := func(family k8snet.IPFamily, expectedIP string) {
		Context(fmt.Sprintf("with IPv%s requested", family), func() {
			It(fmt.Sprintf("should return a valid IPv%s address", family), func() {
				backendConfig := map[string]string{submarinerv1.PublicIP: submarinerv1.API + ":" + t.httpServerURL}
				ip, resolver, err := endpoint.GetPublicIP(family, t.submSpec, fake.NewClientset(), backendConfig, false)

				Expect(err).NotTo(HaveOccurred())
				Expect(ip).To(Equal(expectedIP))
				Expect(resolver).To(Equal(backendConfig[submarinerv1.PublicIP]))
			})
		})
	}

	testGetPublicIP(k8snet.IPv4, testIPv4DNS)
	testGetPublicIP(k8snet.IPv6, testIPv6DNS)
}

func testLoadBalancerResolver() {
	const lbPublicIP = submarinerv1.LoadBalancer + ":" + testServiceName

	t := newResolverTestDriver()

	BeforeEach(func() {
		endpoint.LoadBalancerRetryConfig.Cap = 1 * time.Second
		endpoint.LoadBalancerRetryConfig.Duration = 50 * time.Millisecond
		endpoint.LoadBalancerRetryConfig.Steps = 1
	})

	v4IngressWithIP := v1.LoadBalancerIngress{IP: testIPv4}
	v6IngressWithIP := v1.LoadBalancerIngress{IP: testIPv6}
	ipIngressses := map[k8snet.IPFamily]v1.LoadBalancerIngress{k8snet.IPv4: v4IngressWithIP, k8snet.IPv6: v6IngressWithIP}

	v4IngressWithHostname := v1.LoadBalancerIngress{Hostname: dnsHostv4}
	v6IngressithHostname := v1.LoadBalancerIngress{Hostname: dnsHostv6}
	dnsIPs := map[k8snet.IPFamily]string{k8snet.IPv4: testIPv4DNS, k8snet.IPv6: testIPv6DNS}

	invokeGetPublicIP := func(family k8snet.IPFamily, ingresses ...v1.LoadBalancerIngress) (string, string, error) {
		ip, resolver, err := endpoint.GetPublicIP(family, t.submSpec, fake.NewClientset(&v1.Service{
			ObjectMeta: v1meta.ObjectMeta{
				Name:      testServiceName,
				Namespace: testNamespace,
			},
			Status: v1.ServiceStatus{
				LoadBalancer: v1.LoadBalancerStatus{
					Ingress: ingresses,
				},
			},
		}), map[string]string{submarinerv1.PublicIP: lbPublicIP}, false)

		return ip, resolver, err
	}

	testGetPublicIP := func(family k8snet.IPFamily, expectedIP string, ingresses ...v1.LoadBalancerIngress) {
		ip, resolver, err := invokeGetPublicIP(family, ingresses...)

		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal(expectedIP))
		Expect(resolver).To(Equal(lbPublicIP))
	}

	for _, family := range []k8snet.IPFamily{k8snet.IPv4, k8snet.IPv6} {
		When(fmt.Sprintf("an IPv%s Ingress IP is configured", family), func() {
			It("should return the IP address", func() {
				testGetPublicIP(family, ipIngressses[family].IP, v4IngressWithIP, v6IngressWithIP)
			})
		})

		When(fmt.Sprintf("an IPv%s Ingress Hostname is configured", family), func() {
			It("should return the resolved IP address", func() {
				testGetPublicIP(family, dnsIPs[family], v4IngressWithHostname, v6IngressithHostname)
			})
		})
	}

	When("no Ingress is configured", func() {
		It("should return an error", func() {
			_, _, err := invokeGetPublicIP(k8snet.IPv4)
			Expect(err).To(HaveOccurred())
		})
	})

	When("the Ingress Hostname lookup fails", func() {
		BeforeEach(func() {
			endpoint.LookupIP = func(_ string) ([]net.IP, error) {
				return nil, errors.New("mock error")
			}
		})

		It("should return an error", func() {
			_, _, err := invokeGetPublicIP(k8snet.IPv4, v4IngressWithHostname)
			Expect(err).To(HaveOccurred())
		})
	})

	When("no Ingress resolves", func() {
		BeforeEach(func() {
			endpoint.LookupIP = func(_ string) ([]net.IP, error) {
				return []net.IP{}, nil
			}
		})

		It("should return an error", func() {
			_, _, err := invokeGetPublicIP(k8snet.IPv4, v4IngressWithHostname)
			Expect(err).To(HaveOccurred())
		})
	})
}

func testResolverInAirGapped() {
	t := newResolverTestDriver()

	testGetPublicIP := func(family k8snet.IPFamily, expectedIP string) {
		Context(fmt.Sprintf("with IPv%s requested", family), func() {
			It(fmt.Sprintf("should return a valid IPv%s address", family), func() {
				backendConfig := map[string]string{
					submarinerv1.PublicIP: fmt.Sprintf("%s:bogus,%s:%s,%s:%s", submarinerv1.API, submarinerv1.IPv4, testIPv4,
						submarinerv1.IPv6, testIPv6),
				}
				ip, _, err := endpoint.GetPublicIP(family, t.submSpec, fake.NewClientset(), backendConfig, true)

				Expect(err).NotTo(HaveOccurred())
				Expect(ip).To(Equal(expectedIP))
			})
		})
	}

	testGetPublicIP(k8snet.IPv4, testIPv4)
	testGetPublicIP(k8snet.IPv6, testIPv6)

	When("no resolver succeeds", func() {
		It("should return an empty IP", func() {
			ip, _, err := endpoint.GetPublicIP(k8snet.IPv4, t.submSpec, fake.NewClientset(), map[string]string{
				submarinerv1.PublicIP: submarinerv1.IPv4 + ":",
			}, true)

			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(BeEmpty())
		})
	})

	When("theres no IP family resolver", func() {
		It("should return an empty IP", func() {
			ip, _, err := endpoint.GetPublicIP(k8snet.IPv4, t.submSpec, fake.NewClientset(), map[string]string{
				submarinerv1.PublicIP: submarinerv1.API + ":bogus",
			}, true)

			Expect(err).NotTo(HaveOccurred())
			Expect(ip).To(BeEmpty())
		})
	})
}

func testMultipleResolvers() {
	t := newResolverTestDriver()

	var resolvers []string

	BeforeEach(func() {
		endpoint.LookupIP = func(_ string) ([]net.IP, error) {
			return nil, errors.New("unknown host")
		}

		t.setupHTTPServer(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})

		resolvers = []string{
			submarinerv1.DNS + ":foo",
			submarinerv1.API + ":" + t.httpServerURL,
			submarinerv1.IPv6 + ":" + testIPv6,
			"unknown:unknown",
		}
	})

	It("should return the first successful one", func() {
		working := submarinerv1.IPv4 + ":1.2.3.4"
		backendConfig := map[string]string{submarinerv1.PublicIP: strings.Join(append(resolvers, working), ",")}
		_, resolver, err := endpoint.GetPublicIP(k8snet.IPv4, t.submSpec, fake.NewClientset(), backendConfig, false)

		Expect(err).NotTo(HaveOccurred())
		Expect(resolver).To(Equal(working))
	})

	When("no resolver succeeds", func() {
		It("should return an error", func() {
			backendConfig := map[string]string{submarinerv1.PublicIP: strings.Join(resolvers, ",")}
			_, _, err := endpoint.GetPublicIP(k8snet.IPv4, t.submSpec, fake.NewClientset(), backendConfig, false)

			Expect(err).To(HaveOccurred())
		})
	})
}

type resolverTestDriver struct {
	submSpec      *types.SubmarinerSpecification
	httpServerURL string
}

func newResolverTestDriver() *resolverTestDriver {
	t := &resolverTestDriver{}

	BeforeEach(func() {
		t.submSpec = &types.SubmarinerSpecification{
			Namespace: testNamespace,
		}

		endpoint.LookupIP = func(host string) ([]net.IP, error) {
			if host == dnsHostv4 {
				return []net.IP{net.ParseIP(testIPv4DNS)}, nil
			} else if host == dnsHostv6 {
				return []net.IP{net.ParseIP(testIPv6DNS)}, nil
			}

			return nil, errors.New("unknown host")
		}
	})

	return t
}

func (t *resolverTestDriver) setupHTTPServer(handler http.HandlerFunc) {
	server := httptest.NewServer(handler)

	DeferCleanup(func() {
		server.Close()
	})

	t.httpServerURL = server.URL
}
