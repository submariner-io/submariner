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

package cni_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/cni"
	k8snet "k8s.io/utils/net"
)

const (
	ipV4Addr1 = "192.0.2.1"
	ipV4CIDR1 = ipV4Addr1 + "/24"
	ipV4Addr2 = "10.32.1.0"
	ipV4CIDR2 = ipV4Addr2 + "/24"
	ipV6Addr  = "2000:0:0:1234::"
	ipV6CIDR  = ipV6Addr + "/64"
)

var (
	v4HostInterface1 = cni.HostInterface{
		Name: "v4-iface1",
		Addr: ipV4CIDR1,
	}

	v4HostInterface2 = cni.HostInterface{
		Name: "v4-iface2",
		Addr: ipV4CIDR2,
	}

	v6HostInterface = cni.HostInterface{
		Name: "v6-iface",
		Addr: ipV6CIDR,
	}
)

var _ = Describe("Discover", func() {
	Context("with multiple CIDRs and host interfaces", func() {
		It("should return the correct interface for the requested IP family", func() {
			setupHostInterfaces(v4HostInterface1, v4HostInterface2)

			cniInterface, err := cni.Discover([]string{ipV4CIDR1}, k8snet.IPv4)
			Expect(err).ToNot(HaveOccurred())
			Expect(cniInterface.Name).To(Equal(v4HostInterface1.Name))
			Expect(cniInterface.IPAddress).To(Equal(ipV4Addr1))

			cniInterface, err = cni.Discover([]string{ipV4CIDR2}, k8snet.IPv4)
			Expect(err).ToNot(HaveOccurred())
			Expect(cniInterface.Name).To(Equal(v4HostInterface2.Name))
			Expect(cniInterface.IPAddress).To(Equal(ipV4Addr2))

			setupHostInterfaces(v4HostInterface2)

			cniInterface, err = cni.Discover([]string{ipV4CIDR1, ipV4CIDR2}, k8snet.IPv4)
			Expect(err).ToNot(HaveOccurred())
			Expect(cniInterface.Name).To(Equal(v4HostInterface2.Name))
			Expect(cniInterface.IPAddress).To(Equal(ipV4Addr2))
		})
	})

	Context("with dual-stack", func() {
		It("should return the correct interface for the requested IP family", func() {
			setupHostInterfaces(v4HostInterface1, v6HostInterface)

			cniInterface, err := cni.Discover([]string{ipV4CIDR1, ipV6CIDR}, k8snet.IPv4)
			Expect(err).ToNot(HaveOccurred())
			Expect(cniInterface.Name).To(Equal(v4HostInterface1.Name))
			Expect(cniInterface.IPAddress).To(Equal(ipV4Addr1))

			cniInterface, err = cni.Discover([]string{ipV4CIDR1, ipV6CIDR}, k8snet.IPv6)
			Expect(err).ToNot(HaveOccurred())
			Expect(cniInterface.Name).To(Equal(v6HostInterface.Name))
			Expect(cniInterface.IPAddress).To(Equal(ipV6Addr))
		})
	})

	When("no host interface matches the provided CIDRs", func() {
		It("should return an error", func() {
			setupHostInterfaces(v4HostInterface1)

			_, err := cni.Discover([]string{ipV4CIDR2}, k8snet.IPv4)
			Expect(err).To(HaveOccurred())
		})
	})

	When("the address for a host interface is not a CIDR", func() {
		It("should ignore it", func() {
			setupHostInterfaces(cni.HostInterface{
				Name: "no-cidr",
				Addr: "1.1.1.1",
			}, v4HostInterface1)

			cniInterface, err := cni.Discover([]string{ipV4CIDR1}, k8snet.IPv4)
			Expect(err).ToNot(HaveOccurred())
			Expect(cniInterface.Name).To(Equal(v4HostInterface1.Name))
			Expect(cniInterface.IPAddress).To(Equal(ipV4Addr1))
		})
	})

	Specify("should return an error when HostInterface fails", func() {
		cni.HostInterfaces = func() ([]cni.HostInterface, error) {
			return nil, errors.New("mock error")
		}

		_, err := cni.Discover([]string{ipV4CIDR2}, k8snet.IPv4)
		Expect(err).To(HaveOccurred())
	})
})

func setupHostInterfaces(intf ...cni.HostInterface) {
	cni.HostInterfaces = func() ([]cni.HostInterface, error) {
		return intf, nil
	}
}
