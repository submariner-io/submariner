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
package framework

import (
	"fmt"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/client/informers/externalversions"
)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace for you.
type Framework struct {
	*framework.Framework
}

var SubmarinerClients []*submarinerClientset.Clientset

func init() {
	framework.AddBeforeSuite(beforeSuite)
}

// NewFramework creates a test framework.
func NewFramework(baseName string) *Framework {
	f := &Framework{Framework: framework.NewFramework(baseName)}
	framework.AddCleanupAction(f.GatewayCleanup)

	return f
}

func beforeSuite() {
	framework.By("Creating submariner clients")

	for _, restConfig := range framework.RestConfigs {
		SubmarinerClients = append(SubmarinerClients, createSubmarinerClient(restConfig))
	}

	framework.DetectGlobalnet()
}

func (f *Framework) AwaitGatewayWithStatus(cluster framework.ClusterIndex,
	name string, status submarinerv1.HAStatus) *submarinerv1.Gateway {
	gwClient := SubmarinerClients[cluster].SubmarinerV1().Gateways(framework.TestContext.SubmarinerNamespace)
	gw := framework.AwaitUntil(fmt.Sprintf("await Gateway on %q with status %q", name, status),
		func() (interface{}, error) {
			resGw, err := gwClient.Get(name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return resGw, err
		},
		func(result interface{}) (bool, string, error) {
			if result == nil {
				return false, "gateway not found yet", nil
			}
			gw := result.(*submarinerv1.Gateway)
			if gw.Status.HAStatus != status {
				return false, "", fmt.Errorf("gateway %q exists but has wrong status %q, expected %q",
					gw.Name, gw.Status.HAStatus, status)
			}
			return true, "", nil
		})

	return gw.(*submarinerv1.Gateway)
}

func (f *Framework) AwaitGatewaysWithStatus(
	cluster framework.ClusterIndex, status submarinerv1.HAStatus) []submarinerv1.Gateway {
	gwList := framework.AwaitUntil(fmt.Sprintf("await Gateways with status %q", status),
		func() (interface{}, error) {
			return f.GetGatewaysWithHAStatus(cluster, status), nil
		},
		func(result interface{}) (bool, string, error) {
			gateways := result.([]submarinerv1.Gateway)
			if len(gateways) == 0 {
				return false, "no gateway found yet", nil
			}

			return true, "", nil
		})

	return gwList.([]submarinerv1.Gateway)
}

func (f *Framework) AwaitGatewayRemoved(cluster framework.ClusterIndex, name string) {
	gwClient := SubmarinerClients[cluster].SubmarinerV1().Gateways(framework.TestContext.SubmarinerNamespace)
	framework.AwaitUntil(fmt.Sprintf("await Gateway on %q removed", name),
		func() (interface{}, error) {
			_, err := gwClient.Get(name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		},
		func(result interface{}) (bool, string, error) {
			gone := result.(bool)
			return gone, "", nil
		})
}

func (f *Framework) AwaitGatewayFullyConnected(cluster framework.ClusterIndex, name string) *submarinerv1.Gateway {
	gwClient := SubmarinerClients[cluster].SubmarinerV1().Gateways(framework.TestContext.SubmarinerNamespace)
	gw := framework.AwaitUntil(fmt.Sprintf("await Gateway on %q with status active and connections UP", name),
		func() (interface{}, error) {
			resGw, err := gwClient.Get(name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return resGw, err
		},
		func(result interface{}) (bool, string, error) {
			if result == nil {
				return false, "gateway not found yet", nil
			}
			gw := result.(*submarinerv1.Gateway)
			if gw.Status.HAStatus != submarinerv1.HAStatusActive {
				return false, fmt.Sprintf("Gateway %q exists but not active yet",
					gw.Name), nil
			}
			if len(gw.Status.Connections) == 0 {
				return false, fmt.Sprintf("Gateway %q is active but has no connections yet", name), nil
			}
			for _, conn := range gw.Status.Connections {
				if conn.Status != submarinerv1.Connected {
					return false, fmt.Sprintf("Gateway %q is active but cluster %q is not connected: Status: %q, Message: %q",
						name, conn.Endpoint.ClusterID, conn.Status, conn.StatusMessage), nil
				}
			}

			return true, "", nil
		})

	return gw.(*submarinerv1.Gateway)
}

// GatewayCleanup ensures that only the active gateway node is flagged as gateway node
//                which could not be after a failed test which left the system on an
//                unexpected state
func (f *Framework) GatewayCleanup() {
	for cluster := range SubmarinerClients {
		passiveGateways := f.GetGatewaysWithHAStatus(framework.ClusterIndex(cluster), submarinerv1.HAStatusPassive)

		if len(passiveGateways) == 0 {
			continue
		}

		ginkgo.By(fmt.Sprintf("Cleaning up any non-active gateways: %v", gatewayNames(passiveGateways)))

		for _, nonActiveGw := range passiveGateways {
			f.SetGatewayLabelOnNode(framework.ClusterIndex(cluster), nonActiveGw.Name, false)
			f.AwaitGatewayRemoved(framework.ClusterIndex(cluster), nonActiveGw.Name)
		}
	}
}

func gatewayNames(gateways []submarinerv1.Gateway) []string {
	names := []string{}
	for _, gw := range gateways {
		names = append(names, gw.Name)
	}

	return names
}

func (f *Framework) GetGatewaysWithHAStatus(
	cluster framework.ClusterIndex, status submarinerv1.HAStatus) []submarinerv1.Gateway {
	gatewayClient := SubmarinerClients[cluster].SubmarinerV1().Gateways(
		framework.TestContext.SubmarinerNamespace)
	gwList, err := gatewayClient.List(metav1.ListOptions{})

	filteredGateways := []submarinerv1.Gateway{}
	// List will return "NotFound" if the CRD is not registered in the specific cluster (broker-only)
	if apierrors.IsNotFound(err) {
		return filteredGateways
	}

	Expect(err).NotTo(HaveOccurred())

	for _, gw := range gwList.Items {
		if gw.Status.HAStatus == status {
			filteredGateways = append(filteredGateways, gw)
		}
	}

	return filteredGateways
}

func (f *Framework) DeleteGateway(cluster framework.ClusterIndex, name string) {
	framework.AwaitUntil("delete gateway", func() (interface{}, error) {
		err := SubmarinerClients[cluster].SubmarinerV1().Gateways(
			framework.TestContext.SubmarinerNamespace).Delete(name, &metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}, framework.NoopCheckResult)
}

func (f *Framework) GetGatewayInformer(cluster framework.ClusterIndex) (cache.SharedIndexInformer, chan struct{}) {
	stopCh := make(chan struct{})
	informerFactory := externalversions.NewSharedInformerFactory(SubmarinerClients[cluster], 0)
	informer := informerFactory.Submariner().V1().Gateways().Informer()

	go informer.Run(stopCh)
	Expect(cache.WaitForCacheSync(stopCh, informer.HasSynced)).To(BeTrue())

	return informer, stopCh
}

func GetDeletionChannel(informer cache.SharedIndexInformer) chan string {
	deletionChannel := make(chan string, 100)

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			if object, ok := obj.(metav1.Object); !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				Expect(ok).To(BeTrue(), "tombstone extraction failed")
				object, ok = tombstone.Obj.(metav1.Object)
				Expect(ok).To(BeTrue(), "tombstone inner object extraction failed")
				deletionChannel <- object.GetName()
			} else {
				deletionChannel <- object.GetName()
			}
		},
	})

	return deletionChannel
}
