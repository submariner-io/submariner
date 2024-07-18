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

package libreswan

import (
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fakecommand "github.com/submariner-io/admiral/pkg/command/fake"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

var _ = Describe("Libreswan", func() {
	Describe("IPsec port configuration", testIPsecPortConfiguration)
	Describe("trafficStatusRE", testTrafficStatusRE)
	Describe("ConnectToEndpoint", testConnectToEndpoint)
	Describe("DisconnectFromEndpoint", testDisconnectFromEndpoint)
	Describe("GetConnections", testGetConnections)
})

func testTrafficStatusRE() {
	When("Parsing a normal connection", func() {
		It("should match", func() {
			matches := trafficStatusRE.FindStringSubmatch("006 #3: \"submariner-cable-cluster3-172-17-0-8-0-0\", " +
				"type=ESP, add_time=1590508783, inBytes=0, outBytes=0, id='172.17.0.8'\n")
			Expect(matches).NotTo(BeNil())
		})
	})

	When("Parsing a server-side connection", func() {
		It("should match", func() {
			matches := trafficStatusRE.FindStringSubmatch("006 #2: \"submariner-cable-cluster3-172-17-0-8-0-0\"[1] 3.139.75.179," +
				" type=ESP, add_time=1617195756, inBytes=0, outBytes=0, id='@10.0.63.203-0-0'\n")
			Expect(matches).NotTo(BeNil())
		})
	})
}

func testIPsecPortConfiguration() {
	t := newTestDriver()

	When("NewLibreswan is called with no port environment variables set", func() {
		It("should set the port fields from the defaults in the specification definition", func() {
			Expect(t.driver.ipSecNATTPort).To(Equal(defaultNATTPort))
		})
	})

	When("NewLibreswan is called with port environment variables set", func() {
		const (
			NATTPort       = "4555"
			NATTPortEnvVar = "CE_IPSEC_NATTPORT"
		)

		BeforeEach(func() {
			os.Setenv(NATTPortEnvVar, NATTPort)
		})

		AfterEach(func() {
			os.Unsetenv(NATTPortEnvVar)
		})

		It("should set the port fields from the environment variables", func() {
			Expect(t.driver.ipSecNATTPort).To(Equal(NATTPort))
		})
	})
}

func testConnectToEndpoint() {
	t := newTestDriver()

	var natInfo *natdiscovery.NATEndpointInfo

	BeforeEach(func() {
		natInfo = &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-192-68-2-1",
					PrivateIP: "192.68.2.1",
					Subnets:   []string{"20.0.0.0/16"},
				},
			},
			UseIP:  "172.93.2.1",
			UseNAT: true,
		}
	})

	testBiDirectionalMode := func() {
		ip, err := t.driver.ConnectToEndpoint(natInfo)

		Expect(err).To(Succeed())
		Expect(ip).To(Equal(natInfo.UseIP))

		t.assertActiveConnection(natInfo)
		t.cmdExecutor.AwaitCommand(nil, "whack", t.localEndpoint.PrivateIP, natInfo.UseIP,
			t.localEndpoint.Subnets[0], natInfo.Endpoint.Spec.Subnets[0])
		t.cmdExecutor.AwaitCommand(nil, "whack", "--initiate")
	}

	testServerMode := func() {
		ip, err := t.driver.ConnectToEndpoint(natInfo)

		Expect(err).To(Succeed())
		Expect(ip).To(Equal(natInfo.UseIP))

		t.assertActiveConnection(natInfo)
		t.cmdExecutor.AwaitCommand(nil, "whack", t.localEndpoint.PrivateIP, t.localEndpoint.Subnets[0],
			natInfo.Endpoint.Spec.Subnets[0])
		t.cmdExecutor.EnsureNoCommand("whack", "--initiate")
	}

	When("only the local side prefers to be a server", func() {
		BeforeEach(func() {
			t.localEndpoint.BackendConfig = map[string]string{subv1.PreferredServerConfig: "true"}
			natInfo.Endpoint.Spec.BackendConfig = map[string]string{subv1.PreferredServerConfig: "false"}
		})

		It("should create a server Connection", func() {
			testServerMode()
		})
	})

	When("only the remote side prefers to be a server", func() {
		BeforeEach(func() {
			t.localEndpoint.BackendConfig = map[string]string{subv1.PreferredServerConfig: "false"}
			natInfo.Endpoint.Spec.BackendConfig = map[string]string{subv1.PreferredServerConfig: "true"}
		})

		It("should create a client Connection", func() {
			ip, err := t.driver.ConnectToEndpoint(natInfo)

			Expect(err).To(Succeed())
			Expect(ip).To(Equal(natInfo.UseIP))

			t.assertActiveConnection(natInfo)
			t.cmdExecutor.AwaitCommand(nil, "whack", t.localEndpoint.PrivateIP, natInfo.UseIP,
				t.localEndpoint.Subnets[0], natInfo.Endpoint.Spec.Subnets[0])
			t.cmdExecutor.AwaitCommand(nil, "whack", "--initiate")
		})
	})

	When("neither side prefers to be a server", func() {
		BeforeEach(func() {
			t.localEndpoint.BackendConfig = map[string]string{subv1.PreferredServerConfig: "false"}
			natInfo.Endpoint.Spec.BackendConfig = map[string]string{subv1.PreferredServerConfig: "false"}
		})

		It("should create a bi-directional Connection", func() {
			testBiDirectionalMode()
		})
	})

	When("no preferred server is configured", func() {
		It("should default to a bi-directional Connection", func() {
			testBiDirectionalMode()
		})
	})

	When("both sides prefer to be a server", func() {
		BeforeEach(func() {
			t.localEndpoint.BackendConfig = map[string]string{subv1.PreferredServerConfig: "true"}
			natInfo.Endpoint.Spec.BackendConfig = map[string]string{subv1.PreferredServerConfig: "true"}
		})

		It("should create a server Connection due to comparison of the cable names", func() {
			testServerMode()
		})
	})
}

func testDisconnectFromEndpoint() {
	t := newTestDriver()

	It("should remove the Connection", func() {
		natInfo1 := &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "remote1",
					CableName: "submariner-cable-remote1-192-68-2-1",
					PrivateIP: "192.68.2.1",
					Subnets:   []string{"20.0.0.0/16"},
				},
			},
			UseIP: "172.93.2.1",
		}

		_, err := t.driver.ConnectToEndpoint(natInfo1)
		Expect(err).To(Succeed())

		natInfo2 := &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "remote2",
					CableName: "submariner-cable-remote2-192-68-3-1",
					PrivateIP: "192.68.3.1",
					Subnets:   []string{"30.0.0.0/16"},
				},
			},
			UseIP: "173.93.2.1",
		}

		_, err = t.driver.ConnectToEndpoint(natInfo2)
		Expect(err).To(Succeed())

		Expect(t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: natInfo1.Endpoint.Spec})).To(Succeed())
		t.assertNoActiveConnection(natInfo1)
		t.cmdExecutor.AwaitCommand(nil, "whack", "--delete")
		t.assertActiveConnection(natInfo2)
		t.cmdExecutor.Clear()

		Expect(t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: natInfo2.Endpoint.Spec})).To(Succeed())
		t.assertNoActiveConnection(natInfo2)
		t.cmdExecutor.AwaitCommand(nil, "whack", "--delete")
	})
}

func testGetConnections() {
	t := newTestDriver()

	It("should return the correct Connections", func() {
		natInfo1 := &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "remote1",
					CableName: "submariner-cable-remote1-192-68-2-1",
					PrivateIP: "192.68.2.1",
					Subnets:   []string{"20.0.0.0/16", "30.0.0.0/16"},
				},
			},
			UseIP: "172.93.2.1",
		}

		_, err := t.driver.ConnectToEndpoint(natInfo1)
		Expect(err).To(Succeed())

		natInfo2 := &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "remote2",
					CableName: "submariner-cable-remote2-192-68-3-1",
					PrivateIP: "192.68.3.1",
					Subnets:   []string{"11.0.0.0/16"},
				},
			},
			UseIP: "173.93.3.1",
		}

		_, err = t.driver.ConnectToEndpoint(natInfo2)
		Expect(err).To(Succeed())

		t.cmdExecutor.SetupCommandStdOut(
			fmt.Sprintf(" \"%s-0-0\", type=ESP, add_time=1590508783, inBytes=10, outBytes=20, id='192.68.2.1'",
				natInfo1.Endpoint.Spec.CableName),
			nil, "whack", "--trafficstatus")

		conn, err := t.driver.GetConnections()
		Expect(err).To(Succeed())

		Expect(conn).To(HaveExactElements(subv1.Connection{
			Status:   subv1.Connected,
			Endpoint: natInfo1.Endpoint.Spec,
			UsingIP:  natInfo1.UseIP,
			UsingNAT: natInfo1.UseNAT,
		}, subv1.Connection{
			Status:   subv1.Connecting,
			Endpoint: natInfo2.Endpoint.Spec,
			UsingIP:  natInfo2.UseIP,
			UsingNAT: natInfo2.UseNAT,
		}))
	})
}

type testDriver struct {
	localEndpoint subv1.EndpointSpec
	cmdExecutor   *fakecommand.Executor
	driver        *libreswan
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.cmdExecutor = fakecommand.New()
		t.localEndpoint = subv1.EndpointSpec{
			ClusterID: "local",
			CableName: "submariner-cable-local-192-68-1-1",
			PrivateIP: "192.68.1.1",
			Subnets:   []string{"10.0.0.0/16"},
		}
	})

	JustBeforeEach(func() {
		ls, err := NewLibreswan(endpoint.NewLocal(&t.localEndpoint, dynamicfake.NewSimpleDynamicClient(scheme.Scheme), ""),
			&types.SubmarinerCluster{})
		Expect(err).NotTo(HaveOccurred())

		t.driver = ls.(*libreswan)
		t.driver.plutoStarted = true
	})

	return t
}

func (t *testDriver) assertActiveConnection(natInfo *natdiscovery.NATEndpointInfo) {
	conn, err := t.driver.GetActiveConnections()
	Expect(err).To(Succeed())
	Expect(conn).To(HaveExactElements(subv1.Connection{
		Status:   subv1.Connected,
		Endpoint: natInfo.Endpoint.Spec,
		UsingIP:  natInfo.UseIP,
		UsingNAT: natInfo.UseNAT,
	}))
}

func (t *testDriver) assertNoActiveConnection(natInfo *natdiscovery.NATEndpointInfo) {
	conn, err := t.driver.GetActiveConnections()
	Expect(err).To(Succeed())
	Expect(conn).ToNot(HaveExactElements(subv1.Connection{
		Status:   subv1.Connected,
		Endpoint: natInfo.Endpoint.Spec,
		UsingIP:  natInfo.UseIP,
		UsingNAT: natInfo.UseNAT,
	}))
}
