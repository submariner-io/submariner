/*
© 2021 Red Hat, Inc. and others

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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/submariner-io/admiral/pkg/gomega"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/fake"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/klog"
)

func init() {
	klog.InitFlags(nil)
}

var fakeDriver *fake.Driver

var _ = BeforeSuite(func() {
	cable.AddDriver(fake.DriverName, func(endpoint types.SubmarinerEndpoint, cluster types.SubmarinerCluster) (cable.Driver, error) {
		return fakeDriver, nil
	})
})

var _ = Describe("Cable Engine", func() {
	const localClusterID = "local"
	const remoteClusterID = "remote"

	var (
		engine         cableengine.Engine
		localEndpoint  *types.SubmarinerEndpoint
		remoteEndpoint *types.SubmarinerEndpoint
		skipStart      bool
	)

	BeforeEach(func() {
		skipStart = false

		localEndpoint = &types.SubmarinerEndpoint{
			Spec: subv1.EndpointSpec{
				ClusterID: localClusterID,
				CableName: fmt.Sprintf("submariner-cable-%s-1.1.1.1", localClusterID),
				PrivateIP: "1.1.1.1",
				PublicIP:  "2.2.2.2",
				Backend:   fake.DriverName,
			},
		}

		remoteEndpoint = &types.SubmarinerEndpoint{
			Spec: subv1.EndpointSpec{
				ClusterID: remoteClusterID,
				CableName: fmt.Sprintf("submariner-cable-%s-1.1.1.1", remoteClusterID),
				PrivateIP: "1.1.1.1",
				PublicIP:  "2.2.2.2",
			},
		}

		fakeDriver = fake.New()
		engine = cableengine.NewEngine(types.SubmarinerCluster{
			ID: localClusterID,
			Spec: subv1.ClusterSpec{
				ClusterID: localClusterID,
			},
		}, *localEndpoint)
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
		Expect(engine.GetLocalEndpoint()).To(Equal(localEndpoint))
	})

	When("install cable for a remote endpoint", func() {
		When("no endpoint was previously installed for the cluster", func() {
			It("should connect to the endpoint", func() {
				Expect(engine.InstallCable(*remoteEndpoint)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(remoteEndpoint)
			})
		})

		When("an endpoint was previously installed for the cluster", func() {
			JustBeforeEach(func() {
				Expect(engine.InstallCable(*remoteEndpoint)).To(Succeed())
				fakeDriver.AwaitConnectToEndpoint(remoteEndpoint)

				fakeDriver.ActiveConnections[remoteClusterID] = []subv1.Connection{{Endpoint: remoteEndpoint.Spec}}
			})

			When("it's a different endpoint", func() {
				It("should disconnect from the previous endpoint and connect to the new one", func() {
					newEndpoint := *remoteEndpoint
					newEndpoint.Spec.CableName = "new cable"

					Expect(engine.InstallCable(newEndpoint)).To(Succeed())
					fakeDriver.AwaitDisconnectFromEndpoint(remoteEndpoint)
					fakeDriver.AwaitConnectToEndpoint(&newEndpoint)
				})
			})

			When("it's the same endpoint", func() {
				It("should not connect to the endpoint again", func() {
					Expect(engine.InstallCable(*remoteEndpoint)).To(Succeed())
					fakeDriver.AwaitNoConnectToEndpoint()
				})
			})

			When("it's the same endpoint but with a different public IP", func() {
				It("should disconnect from the previous endpoint and connect to the new one", func() {
					prevEndpoint := *remoteEndpoint
					remoteEndpoint.Spec.PublicIP = "3.3.3.3"

					Expect(engine.InstallCable(*remoteEndpoint)).To(Succeed())
					fakeDriver.AwaitDisconnectFromEndpoint(&prevEndpoint)
					fakeDriver.AwaitConnectToEndpoint(remoteEndpoint)
				})

				When("the driver fails to disconnect from the endpoint", func() {
					JustBeforeEach(func() {
						fakeDriver.ErrOnDisconnectFromEndpoint = errors.New("fake disconnect error")
					})

					It("should return an error", func() {
						remoteEndpoint.Spec.PublicIP = "3.3.3.3"

						Expect(engine.InstallCable(*remoteEndpoint)).To(ContainErrorSubstring(fakeDriver.ErrOnDisconnectFromEndpoint))
						fakeDriver.AwaitNoConnectToEndpoint()
					})
				})
			})

			When("retrieval of the driver's active connections fails", func() {
				JustBeforeEach(func() {
					fakeDriver.ActiveConnections[remoteClusterID] = errors.New("fake active connections error")
				})

				It("should return an error", func() {
					Expect(engine.InstallCable(*remoteEndpoint)).To(ContainErrorSubstring(
						fakeDriver.ActiveConnections[remoteClusterID].(error)))
				})
			})
		})

		When("the driver fails to connect to the endpoint", func() {
			JustBeforeEach(func() {
				fakeDriver.ErrOnConnectToEndpoint = errors.New("fake connect error")
			})

			It("should return an error", func() {
				Expect(engine.InstallCable(*remoteEndpoint)).To(ContainErrorSubstring(fakeDriver.ErrOnConnectToEndpoint))
			})
		})

		When("retrieval of the driver's active connections fails", func() {
			JustBeforeEach(func() {
				fakeDriver.ActiveConnections[remoteClusterID] = errors.New("fake active connections error")
			})

			It("should return an error", func() {
				Expect(engine.InstallCable(*remoteEndpoint)).To(ContainErrorSubstring(
					fakeDriver.ActiveConnections[remoteClusterID].(error)))
			})
		})
	})

	When("install cable for a local endpoint", func() {
		It("should not connect to the endpoint", func() {
			Expect(engine.InstallCable(*localEndpoint)).To(Succeed())
			fakeDriver.AwaitNoConnectToEndpoint()
		})
	})

	When("remove cable for a remote endpoint", func() {
		JustBeforeEach(func() {
			Expect(engine.InstallCable(*remoteEndpoint)).To(Succeed())
			fakeDriver.AwaitConnectToEndpoint(remoteEndpoint)
		})

		It("should disconnect from the endpoint", func() {
			Expect(engine.RemoveCable(*remoteEndpoint)).To(Succeed())
			fakeDriver.AwaitDisconnectFromEndpoint(remoteEndpoint)
		})

		When("the driver fails to disconnect from the endpoint", func() {
			JustBeforeEach(func() {
				fakeDriver.ErrOnDisconnectFromEndpoint = errors.New("fake disconnect error")
			})

			It("should return an error", func() {
				Expect(engine.RemoveCable(*remoteEndpoint)).To(ContainErrorSubstring(fakeDriver.ErrOnDisconnectFromEndpoint))
			})
		})
	})

	When("remove cable for a local endpoint", func() {
		JustBeforeEach(func() {
			Expect(engine.InstallCable(*remoteEndpoint)).To(Succeed())
			fakeDriver.AwaitConnectToEndpoint(remoteEndpoint)
		})

		It("should not disconnect from the endpoint", func() {
			Expect(engine.RemoveCable(*localEndpoint)).To(Succeed())
			fakeDriver.AwaitNoDisconnectFromEndpoint()
		})
	})

	When("list cable connections", func() {
		BeforeEach(func() {
			fakeDriver.Connections = []subv1.Connection{{Endpoint: remoteEndpoint.Spec}}
		})

		It("should retrieve the connections from the driver", func() {
			Expect(engine.ListCableConnections()).To(Equal(fakeDriver.Connections))
		})

		When("retrieval of the driver's connections fails", func() {
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

		When("the HA status is queried", func() {
			It("should return passive", func() {
				Expect(engine.GetHAStatus()).To(Equal(subv1.HAStatusPassive))
			})
		})

		When("list cable connections", func() {
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
