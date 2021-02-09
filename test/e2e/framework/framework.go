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
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

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
	return &Framework{Framework: framework.NewFramework(baseName)}
}

func beforeSuite() {
	framework.By("Creating submariner clients")

	for _, restConfig := range framework.RestConfigs {
		SubmarinerClients = append(SubmarinerClients, createSubmarinerClient(restConfig))
	}

	framework.DetectGlobalnet()
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
