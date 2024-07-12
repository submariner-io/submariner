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

package ovn_test

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("TransitSwitchIP", func() {
	var (
		transitSwitchIP ovn.TransitSwitchIP
		nodeIP          string
		node            *corev1.Node
	)

	BeforeEach(func() {
		transitSwitchIP = ovn.NewTransitSwitchIP()
		nodeIP = ""
	})

	Context("Init", func() {
		var k8sClient *fakek8s.Clientset

		JustBeforeEach(func() {
			k8sClient = fakek8s.NewSimpleClientset()
			node = createNode(k8sClient, nodeIP)
		})

		When("the node annotation exists", func() {
			BeforeEach(func() {
				nodeIP = "172.1.2.3"
			})

			It("should set the TransitSwitchIP value", func() {
				Expect(transitSwitchIP.Init(k8sClient)).To(Succeed())
				Expect(transitSwitchIP.Get()).To(Equal(nodeIP))
			})
		})

		When("the node annotation does not exist", func() {
			It("should succeed and set an empty TransitSwitchIP value", func() {
				Expect(transitSwitchIP.Init(k8sClient)).To(Succeed())
				Expect(transitSwitchIP.Get()).To(Equal(""))
			})
		})

		When("the local node isn't found due to missing NODE_NAME env var", func() {
			JustBeforeEach(func() {
				os.Unsetenv("NODE_NAME")
			})

			It("should fail", func() {
				Expect(transitSwitchIP.Init(k8sClient)).ToNot(Succeed())
			})
		})

		When("the node annotation contains an invalid value", func() {
			JustBeforeEach(func() {
				node.Annotations = map[string]string{constants.OvnTransitSwitchIPAnnotation: "invalid"}
				_, err := k8sClient.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
				Expect(err).To(Succeed())
			})

			It("should fail", func() {
				Expect(transitSwitchIP.Init(k8sClient)).ToNot(Succeed())
			})
		})
	})

	Context("UpdateFrom", func() {
		localNodeName := "local-node"

		BeforeEach(func() {
			os.Setenv("NODE_NAME", localNodeName)

			nodeIP = "172.1.2.3"
			node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        localNodeName,
					Annotations: map[string]string{constants.OvnTransitSwitchIPAnnotation: toTransitSwitchIPAnnotation(nodeIP)},
				},
			}
		})

		When("the local node", func() {
			It("should set the TransitSwitchIP value", func() {
				updated, err := transitSwitchIP.UpdateFrom(node)
				Expect(err).To(Succeed())
				Expect(updated).To(BeTrue())
				Expect(transitSwitchIP.Get()).To(Equal(nodeIP))

				updated, err = transitSwitchIP.UpdateFrom(node)
				Expect(err).To(Succeed())
				Expect(updated).To(BeFalse())
			})
		})

		When("not the local node", func() {
			BeforeEach(func() {
				node.Name = "not-local"
			})

			It("should not set the TransitSwitchIP value", func() {
				updated, err := transitSwitchIP.UpdateFrom(node)
				Expect(err).To(Succeed())
				Expect(updated).To(BeFalse())
				Expect(transitSwitchIP.Get()).To(Equal(""))
			})
		})
	})
})
