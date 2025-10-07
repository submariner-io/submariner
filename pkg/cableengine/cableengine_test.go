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

package cableengine_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/certificate"
	. "github.com/submariner-io/admiral/pkg/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/fake"
	"github.com/submariner-io/submariner/pkg/cableengine"
	submendpoint "github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
)

func init() {
	kzerolog.AddFlags(nil)
}

var fakeDriver *fake.Driver

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
	cable.AddDriver(fake.DriverName, func(_ *submendpoint.Local, _ *types.SubmarinerCluster,
		_ certificate.SigningRequestor,
	) (cable.Driver, error) {
		return fakeDriver, nil
	})
})

const (
	localClusterID  = "local"
	remoteClusterID = "remote"
)

var _ = Describe("Cable Engine", func() {
	t := newTestDriver()

	It("should return the local endpoint when queried", func() {
		Expect(t.engine.GetLocalEndpoint()).To(Equal(&types.SubmarinerEndpoint{Spec: t.localEndpoint.Spec}))
	})

	t.testRemoteEndpoint(k8snet.IPv4)
	t.testRemoteEndpoint(k8snet.IPv6)

	When("install cable for a local endpoint", func() {
		It("should not connect to the endpoint", func() {
			Expect(t.engine.InstallCable(t.localEndpoint, k8snet.IPv4)).To(Succeed())
			fakeDriver.AwaitNoConnectToEndpoint()
		})
	})

	When("remove cable for a local endpoint", func() {
		JustBeforeEach(func() {
			Expect(t.engine.InstallCable(t.remoteEndpoint, k8snet.IPv4)).To(Succeed())
			fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(t.remoteEndpoint, k8snet.IPv4))
		})

		It("should not disconnect from the endpoint", func() {
			Expect(t.engine.RemoveCable(t.localEndpoint, k8snet.IPv4)).To(Succeed())
			fakeDriver.AwaitNoDisconnectFromEndpoint()
			Consistently(t.natDiscovery.removeEndpoint).ShouldNot(Receive())
		})
	})

	When("list cable connections", func() {
		BeforeEach(func() {
			fakeDriver.Connections = []subv1.Connection{{Endpoint: t.remoteEndpoint.Spec}}
		})

		It("should retrieve the connections from the driver", func() {
			Expect(t.engine.ListCableConnections()).To(Equal(fakeDriver.Connections))
		})

		Context("and retrieval of the driver's connections fails", func() {
			JustBeforeEach(func() {
				fakeDriver.Connections = errors.New("fake connections error")
			})

			It("should return an error", func() {
				_, err := t.engine.ListCableConnections()
				Expect(err).To(ContainErrorSubstring(fakeDriver.Connections.(error)))
			})
		})
	})

	When("the HA status is queried", func() {
		It("should return active", func() {
			Expect(t.engine.GetHAStatus()).To(Equal(subv1.HAStatusActive))
		})
	})

	When("driver initialization fails", func() {
		BeforeEach(func() {
			fakeDriver.ErrOnInit = errors.New("fake init error")
		})

		It("should fail to start", func() {
		})
	})

	When("not started", func() {
		BeforeEach(func() {
			t.skipStart = true
		})

		Context("and the HA status is queried", func() {
			It("should return passive", func() {
				Expect(t.engine.GetHAStatus()).To(Equal(subv1.HAStatusPassive))
			})
		})

		Context("and list of cable connections is queried", func() {
			It("should return non-nil", func() {
				Expect(t.engine.ListCableConnections()).ToNot(BeNil())
			})
		})
	})

	When("after Stop is called", func() {
		BeforeEach(func() {
			fakeDriver.Connections = []subv1.Connection{{Endpoint: t.remoteEndpoint.Spec}}
		})

		JustBeforeEach(func() {
			t.engine.Stop()
		})

		Context("and the HA status is queried", func() {
			It("should return passive", func() {
				Expect(t.engine.GetHAStatus()).To(Equal(subv1.HAStatusPassive))
			})
		})

		Context("and list of cable connections is queried", func() {
			It("should return empty", func() {
				Expect(t.engine.ListCableConnections()).To(BeEmpty())
			})
		})
	})
})

func TestCableEngine(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cable Engine Suite")
}

type testDriver struct {
	engine         cableengine.Engine
	natDiscovery   *fakeNATDiscovery
	localEndpoint  *subv1.Endpoint
	remoteEndpoint *subv1.Endpoint
	skipStart      bool
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.skipStart = false

		t.localEndpoint = &subv1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Now(),
			},
			Spec: subv1.EndpointSpec{
				ClusterID:  localClusterID,
				CableName:  fmt.Sprintf("submariner-cable-%s-1.1.1.1", localClusterID),
				PrivateIPs: []string{"1.1.1.1", "FDC8:BF8B:E62C:ABCD:1111:2222:3333:4444"},
				PublicIPs:  []string{"2.2.2.2", "FDC8:BF8B:E62C:ABCD:1111:2222:3333:5555"},
				Backend:    fake.DriverName,
			},
		}

		t.remoteEndpoint = &subv1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Now(),
			},
			Spec: subv1.EndpointSpec{
				ClusterID:     remoteClusterID,
				CableName:     fmt.Sprintf("submariner-cable-%s-1.1.1.1", remoteClusterID),
				PrivateIPs:    []string{"1.1.1.1", "FDC7:BF8B:E62C:ABCD:1111:2222:3333:4444"},
				PublicIPs:     []string{"2.2.2.2", "FDC7:BF8B:E62C:ABCD:1111:2222:3333:5555"},
				BackendConfig: map[string]string{"port": "1234"},
			},
		}

		fakeDriver = fake.New()
		t.engine = cableengine.NewEngine(&types.SubmarinerCluster{
			ID: localClusterID,
			Spec: subv1.ClusterSpec{
				ClusterID: localClusterID,
			},
		}, submendpoint.NewLocal(&t.localEndpoint.Spec, dynamicfake.NewSimpleDynamicClient(scheme.Scheme), ""))

		t.natDiscovery = &fakeNATDiscovery{removeEndpoint: make(chan string, 20), readyChannel: make(chan *natdiscovery.NATEndpointInfo, 100)}
		t.engine.SetupNATDiscovery(t.natDiscovery)
	})

	JustBeforeEach(func() {
		if t.skipStart {
			return
		}

		err := t.engine.StartEngine(context.TODO(), nil)
		if fakeDriver.ErrOnInit != nil {
			Expect(err).To(ContainErrorSubstring(fakeDriver.ErrOnInit))
		} else {
			Expect(err).To(Succeed())
			fakeDriver.AwaitInit()
		}
	})

	return t
}

func (t *testDriver) testRemoteEndpoint(ipFamily k8snet.IPFamily) {
	When(fmt.Sprintf("install cable for an IPv%v remote endpoint", ipFamily), func() {
		Context("and no endpoint was previously installed for the cluster", func() {
			It("should connect to the endpoint", func() {
				Expect(t.engine.InstallCable(t.remoteEndpoint, ipFamily)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(t.remoteEndpoint, ipFamily))
			})
		})

		Context("and an endpoint was previously installed for the cluster", func() {
			var prevEndpoint *subv1.Endpoint
			var newEndpoint *subv1.Endpoint

			BeforeEach(func() {
				c := *t.remoteEndpoint
				newEndpoint = &c
				prevEndpoint = t.remoteEndpoint
			})

			JustBeforeEach(func() {
				Expect(t.engine.InstallCable(prevEndpoint, ipFamily)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(prevEndpoint, ipFamily))

				Expect(t.engine.InstallCable(newEndpoint, ipFamily)).To(Succeed())
			})

			testTimestamps := func() {
				Context("and older creation timestamp", func() {
					BeforeEach(func() {
						newEndpoint.CreationTimestamp = metav1.Time{Time: metav1.Now().Add(100 * time.Millisecond)}
					})

					It("should disconnect from the previous endpoint and connect to the new one", func() {
						fakeDriver.AwaitDisconnectFromEndpoint(&prevEndpoint.Spec, ipFamily)
						fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(newEndpoint, ipFamily))
					})
				})

				Context("and newer creation timestamp", func() {
					BeforeEach(func() {
						newEndpoint.CreationTimestamp = metav1.Now()
						prevEndpoint.CreationTimestamp = metav1.Time{Time: newEndpoint.CreationTimestamp.Add(100 * time.Millisecond)}
					})

					It("should not disconnect from the previous endpoint nor connect to the new one", func() {
						fakeDriver.AwaitNoDisconnectFromEndpoint()
						fakeDriver.AwaitNoConnectToEndpoint()
					})
				})
			}

			Context("with a different cable name", func() {
				BeforeEach(func() {
					newEndpoint.Spec.CableName = "new cable"
				})

				testTimestamps()
			})

			Context("with the same cable name", func() {
				testTimestamps()

				Context("but different endpoint IP", func() {
					BeforeEach(func() {
						newEndpoint.Spec.PublicIPs = []string{"3.3.3.3", "FDC7:BF8B:E62C:ABCD:1111:2222:3333:7777"}
					})

					It("should disconnect from the previous endpoint and connect to the new one", func() {
						fakeDriver.AwaitDisconnectFromEndpoint(&prevEndpoint.Spec, ipFamily)
						fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(newEndpoint, ipFamily))
					})
				})

				Context("but different backend configuration", func() {
					BeforeEach(func() {
						newEndpoint.Spec.BackendConfig = map[string]string{"port": "6789"}
					})

					It("should disconnect from the previous endpoint and connect to the new one", func() {
						fakeDriver.AwaitDisconnectFromEndpoint(&prevEndpoint.Spec, ipFamily)
						fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(newEndpoint, ipFamily))
					})
				})

				Context("and connection info", func() {
					It("should not disconnect from the previous endpoint nor connect to the new one", func() {
						fakeDriver.AwaitNoDisconnectFromEndpoint()
						fakeDriver.AwaitNoConnectToEndpoint()
					})
				})
			})
		})

		Context("and an endpoint was previously installed for another cluster", func() {
			It("should connect to the new endpoint and not disconnect from the previous one", func() {
				otherEndpoint := subv1.Endpoint{Spec: subv1.EndpointSpec{
					ClusterID: "other",
					CableName: "submariner-cable-other-1.1.1.1",
				}}

				Expect(t.engine.InstallCable(&otherEndpoint, ipFamily)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(&otherEndpoint, ipFamily))

				Expect(t.engine.InstallCable(t.remoteEndpoint, ipFamily)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(t.remoteEndpoint, ipFamily))
				fakeDriver.AwaitNoDisconnectFromEndpoint()
			})
		})

		Context("followed by remove cable before NAT discovery is complete", func() {
			BeforeEach(func() {
				t.natDiscovery.captureAddEndpoint = make(chan *subv1.Endpoint, 10)
			})

			It("should not connect to the endpoint", func() {
				Expect(t.engine.InstallCable(t.remoteEndpoint, ipFamily)).To(Succeed())
				Eventually(t.natDiscovery.captureAddEndpoint).Should(Receive())

				Expect(t.engine.RemoveCable(t.remoteEndpoint, ipFamily)).To(Succeed())
				Eventually(t.natDiscovery.removeEndpoint).Should(Receive(Equal(t.remoteEndpoint.Spec.GetFamilyCableName(ipFamily))))
				fakeDriver.AwaitNoDisconnectFromEndpoint()

				t.natDiscovery.notifyReady(t.remoteEndpoint, ipFamily)
				fakeDriver.AwaitNoConnectToEndpoint()
			})
		})
	})

	When(fmt.Sprintf("remove cable for an IPv%v remote endpoint", ipFamily), func() {
		JustBeforeEach(func() {
			Expect(t.engine.InstallCable(t.remoteEndpoint, ipFamily)).To(Succeed())
			fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(t.remoteEndpoint, ipFamily))
		})

		It("should disconnect from the endpoint", func() {
			Expect(t.engine.RemoveCable(t.remoteEndpoint, ipFamily)).To(Succeed())
			fakeDriver.AwaitDisconnectFromEndpoint(&t.remoteEndpoint.Spec, ipFamily)
			Eventually(t.natDiscovery.removeEndpoint).Should(Receive(Equal(t.remoteEndpoint.Spec.GetFamilyCableName(ipFamily))))
		})

		Context("and the driver fails to disconnect from the endpoint", func() {
			JustBeforeEach(func() {
				fakeDriver.ErrOnDisconnectFromEndpoint = errors.New("fake disconnect error")
			})

			It("should return an error", func() {
				Expect(t.engine.RemoveCable(t.remoteEndpoint, ipFamily)).To(HaveOccurred())
			})
		})
	})
}

type fakeNATDiscovery struct {
	removeEndpoint     chan string
	captureAddEndpoint chan *subv1.Endpoint
	readyChannel       chan *natdiscovery.NATEndpointInfo
}

func (n *fakeNATDiscovery) Run(_ <-chan struct{}) error {
	return nil
}

func (n *fakeNATDiscovery) AddEndpoint(endpoint *subv1.Endpoint, family k8snet.IPFamily) {
	if n.captureAddEndpoint != nil {
		n.captureAddEndpoint <- endpoint
		return
	}

	n.notifyReady(endpoint, family)
}

func (n *fakeNATDiscovery) RemoveEndpoint(endpointName string) {
	n.removeEndpoint <- endpointName
}

func (n *fakeNATDiscovery) GetReadyChannel() chan *natdiscovery.NATEndpointInfo {
	return n.readyChannel
}

func (n *fakeNATDiscovery) notifyReady(endpoint *subv1.Endpoint, family k8snet.IPFamily) {
	n.readyChannel <- natEndpointInfoFor(endpoint, family)
}

func natEndpointInfoFor(endpoint *subv1.Endpoint, family k8snet.IPFamily) *natdiscovery.NATEndpointInfo {
	return &natdiscovery.NATEndpointInfo{
		UseIP:     endpoint.Spec.GetPublicIP(family),
		UseNAT:    true,
		Endpoint:  *endpoint,
		UseFamily: family,
	}
}
