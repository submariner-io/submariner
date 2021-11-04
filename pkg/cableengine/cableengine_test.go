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
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/submariner-io/admiral/pkg/gomega"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/fake"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

func init() {
	klog.InitFlags(nil)
}

var fakeDriver *fake.Driver

var _ = BeforeSuite(func() {
	cable.AddDriver(fake.DriverName, func(endpoint *types.SubmarinerEndpoint, cluster *types.SubmarinerCluster) (cable.Driver, error) {
		return fakeDriver, nil
	})
})

var _ = Describe("Cable Engine", func() {
	const localClusterID = "local"
	const remoteClusterID = "remote"

	var (
		engine         cableengine.Engine
		natDiscovery   *fakeNATDiscovery
		localEndpoint  *subv1.Endpoint
		remoteEndpoint *subv1.Endpoint
		skipStart      bool
	)

	BeforeEach(func() {
		skipStart = false

		localEndpoint = &subv1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Now(),
			},
			Spec: subv1.EndpointSpec{
				ClusterID: localClusterID,
				CableName: fmt.Sprintf("submariner-cable-%s-1.1.1.1", localClusterID),
				PrivateIP: "1.1.1.1",
				PublicIP:  "2.2.2.2",
				Backend:   fake.DriverName,
			},
		}

		remoteEndpoint = &subv1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Now(),
			},
			Spec: subv1.EndpointSpec{
				ClusterID:     remoteClusterID,
				CableName:     fmt.Sprintf("submariner-cable-%s-1.1.1.1", remoteClusterID),
				PrivateIP:     "1.1.1.1",
				PublicIP:      "2.2.2.2",
				BackendConfig: map[string]string{"port": "1234"},
			},
		}

		fakeDriver = fake.New()
		engine = cableengine.NewEngine(&types.SubmarinerCluster{
			ID: localClusterID,
			Spec: subv1.ClusterSpec{
				ClusterID: localClusterID,
			},
		}, &types.SubmarinerEndpoint{Spec: localEndpoint.Spec})

		natDiscovery = &fakeNATDiscovery{removeEndpoint: make(chan string, 20), readyChannel: make(chan *natdiscovery.NATEndpointInfo, 100)}
		engine.SetupNATDiscovery(natDiscovery)
	})

	JustBeforeEach(func() {
		if skipStart {
			return
		}

		err := engine.StartEngine()
		if fakeDriver.ErrOnInit != nil {
			Expect(err).To(ContainErrorSubstring(fakeDriver.ErrOnInit))
		} else {
			Expect(err).To(Succeed())
			fakeDriver.AwaitInit()
		}
	})

	It("should return the local endpoint when queried", func() {
		Expect(engine.GetLocalEndpoint()).To(Equal(&types.SubmarinerEndpoint{Spec: localEndpoint.Spec}))
	})

	When("install cable for a remote endpoint", func() {
		Context("and no endpoint was previously installed for the cluster", func() {
			It("should connect to the endpoint", func() {
				Expect(engine.InstallCable(remoteEndpoint)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(remoteEndpoint))
			})
		})

		Context("and an endpoint was previously installed for the cluster", func() {
			var prevEndpoint *subv1.Endpoint
			var newEndpoint *subv1.Endpoint

			BeforeEach(func() {
				copy := *remoteEndpoint
				newEndpoint = &copy
				prevEndpoint = remoteEndpoint
			})

			JustBeforeEach(func() {
				Expect(engine.InstallCable(prevEndpoint)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(prevEndpoint))

				Expect(engine.InstallCable(newEndpoint)).To(Succeed())
			})

			testTimestamps := func() {
				Context("and older creation timestamp", func() {
					BeforeEach(func() {
						time.Sleep(100 * time.Millisecond)
						newEndpoint.CreationTimestamp = metav1.Now()
					})

					It("should disconnect from the previous endpoint and connect to the new one", func() {
						fakeDriver.AwaitDisconnectFromEndpoint(&prevEndpoint.Spec)
						fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(newEndpoint))
					})
				})

				Context("and newer creation timestamp", func() {
					BeforeEach(func() {
						newEndpoint.CreationTimestamp = metav1.Now()
						time.Sleep(100 * time.Millisecond)
						prevEndpoint.CreationTimestamp = metav1.Now()
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
						newEndpoint.Spec.PublicIP = "3.3.3.3"
					})

					It("should disconnect from the previous endpoint and connect to the new one", func() {
						fakeDriver.AwaitDisconnectFromEndpoint(&prevEndpoint.Spec)
						fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(newEndpoint))
					})
				})

				Context("but different backend configuration", func() {
					BeforeEach(func() {
						newEndpoint.Spec.BackendConfig = map[string]string{"port": "6789"}
					})

					It("should disconnect from the previous endpoint and connect to the new one", func() {
						fakeDriver.AwaitDisconnectFromEndpoint(&prevEndpoint.Spec)
						fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(newEndpoint))
					})
				})

				Context(" and connection info", func() {
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

				Expect(engine.InstallCable(&otherEndpoint)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(&otherEndpoint))

				Expect(engine.InstallCable(remoteEndpoint)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(remoteEndpoint))
				fakeDriver.AwaitNoDisconnectFromEndpoint()
			})
		})

		Context("followed by remove cable before NAT discovery is complete", func() {
			BeforeEach(func() {
				natDiscovery.captureAddEndpoint = make(chan *subv1.Endpoint, 10)
			})

			It("should not connect to the endpoint", func() {
				Expect(engine.InstallCable(remoteEndpoint)).To(Succeed())
				Eventually(natDiscovery.captureAddEndpoint).Should(Receive())

				Expect(engine.RemoveCable(remoteEndpoint)).To(Succeed())
				Eventually(natDiscovery.removeEndpoint).Should(Receive(Equal(remoteEndpoint.Spec.CableName)))
				fakeDriver.AwaitNoDisconnectFromEndpoint()

				natDiscovery.notifyReady(remoteEndpoint)
				fakeDriver.AwaitNoConnectToEndpoint()
			})
		})
	})

	When("install cable for a local endpoint", func() {
		It("should not connect to the endpoint", func() {
			Expect(engine.InstallCable(localEndpoint)).To(Succeed())
			fakeDriver.AwaitNoConnectToEndpoint()
		})
	})

	When("remove cable for a remote endpoint", func() {
		JustBeforeEach(func() {
			Expect(engine.InstallCable(remoteEndpoint)).To(Succeed())
			fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(remoteEndpoint))
		})

		It("should disconnect from the endpoint", func() {
			Expect(engine.RemoveCable(remoteEndpoint)).To(Succeed())
			fakeDriver.AwaitDisconnectFromEndpoint(&remoteEndpoint.Spec)
			Eventually(natDiscovery.removeEndpoint).Should(Receive(Equal(remoteEndpoint.Spec.CableName)))
		})

		Context("and the driver fails to disconnect from the endpoint", func() {
			JustBeforeEach(func() {
				fakeDriver.ErrOnDisconnectFromEndpoint = errors.New("fake disconnect error")
			})

			It("should return an error", func() {
				Expect(engine.RemoveCable(remoteEndpoint)).To(HaveOccurred())
			})
		})
	})

	When("remove cable for a local endpoint", func() {
		JustBeforeEach(func() {
			Expect(engine.InstallCable(remoteEndpoint)).To(Succeed())
			fakeDriver.AwaitConnectToEndpoint(natEndpointInfoFor(remoteEndpoint))
		})

		It("should not disconnect from the endpoint", func() {
			Expect(engine.RemoveCable(localEndpoint)).To(Succeed())
			fakeDriver.AwaitNoDisconnectFromEndpoint()
			Consistently(natDiscovery.removeEndpoint).ShouldNot(Receive())
		})
	})

	When("list cable connections", func() {
		BeforeEach(func() {
			fakeDriver.Connections = []subv1.Connection{{Endpoint: remoteEndpoint.Spec}}
		})

		It("should retrieve the connections from the driver", func() {
			Expect(engine.ListCableConnections()).To(Equal(fakeDriver.Connections))
		})

		Context("and retrieval of the driver's connections fails", func() {
			JustBeforeEach(func() {
				fakeDriver.Connections = errors.New("fake connections error")
			})

			It("should return an error", func() {
				_, err := engine.ListCableConnections()
				Expect(err).To(ContainErrorSubstring(fakeDriver.Connections.(error)))
			})
		})
	})

	When("the HA status is queried", func() {
		It("should return active", func() {
			Expect(engine.GetHAStatus()).To(Equal(subv1.HAStatusActive))
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
			skipStart = true
		})

		Context("and the HA status is queried", func() {
			It("should return passive", func() {
				Expect(engine.GetHAStatus()).To(Equal(subv1.HAStatusPassive))
			})
		})

		Context("and list cable connections", func() {
			It("should return non-nil", func() {
				Expect(engine.ListCableConnections()).ToNot(BeNil())
			})
		})
	})
})

func TestCableEngine(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cable Engine Suite")
}

type fakeNATDiscovery struct {
	removeEndpoint     chan string
	captureAddEndpoint chan *subv1.Endpoint
	readyChannel       chan *natdiscovery.NATEndpointInfo
}

func (n *fakeNATDiscovery) Run(stopCh <-chan struct{}) error {
	return nil
}

func (n *fakeNATDiscovery) AddEndpoint(endpoint *subv1.Endpoint) {
	if n.captureAddEndpoint != nil {
		n.captureAddEndpoint <- endpoint
		return
	}

	n.notifyReady(endpoint)
}

func (n *fakeNATDiscovery) RemoveEndpoint(endpointName string) {
	n.removeEndpoint <- endpointName
}

func (n *fakeNATDiscovery) GetReadyChannel() chan *natdiscovery.NATEndpointInfo {
	return n.readyChannel
}

func (n *fakeNATDiscovery) notifyReady(endpoint *subv1.Endpoint) {
	n.readyChannel <- natEndpointInfoFor(endpoint)
}

func natEndpointInfoFor(endpoint *subv1.Endpoint) *natdiscovery.NATEndpointInfo {
	return &natdiscovery.NATEndpointInfo{
		UseIP:    endpoint.Spec.PublicIP,
		UseNAT:   true,
		Endpoint: *endpoint,
	}
}
