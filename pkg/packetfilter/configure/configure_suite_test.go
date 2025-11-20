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

package configure_test

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/global"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/packetfilter/configure"
	"github.com/submariner-io/submariner/pkg/packetfilter/iptables"
	"github.com/submariner-io/submariner/pkg/packetfilter/nftables"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const defaultDriver = configure.NfTables

var _ = Describe("DriverFromGlobalConfig", func() {
	var cm *corev1.ConfigMap

	BeforeEach(func() {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "config",
			},
			Data: map[string]string{},
		}
	})

	JustBeforeEach(func() {
		global.Init(cm)
	})

	When(configure.UseNftablesKey+" key is present", func() {
		Context("and set to true", func() {
			BeforeEach(func() {
				cm.Data[configure.UseNftablesKey] = "true"
			})

			It("should set the nftables driver", func() {
				configure.DriverFromGlobalConfig()
				verifyDriverFn(configure.NfTables)
			})
		})

		Context("and set to false", func() {
			BeforeEach(func() {
				cm.Data[configure.UseNftablesKey] = "false"
			})

			It("should set the iptables driver", func() {
				configure.DriverFromGlobalConfig()
				verifyDriverFn(configure.IPTables)
			})
		})
	})

	When(configure.UseNftablesKey+" key is not present", func() {
		It("should set the default driver", func() {
			configure.DriverFromGlobalConfig()
			verifyDriverFn(defaultDriver)
		})
	})
})

func verifyDriverFn(dType configure.DriverType) {
	fnValue := func(v any) string {
		return fmt.Sprintf("%v", v)
	}

	if dType == configure.NfTables {
		Expect(fnValue(packetfilter.GetNewDriverFn())).To(Equal(fnValue(nftables.New)))
	} else {
		Expect(fnValue(packetfilter.GetNewDriverFn())).To(Equal(fnValue(iptables.New)))
	}
}

func TestConfigure(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Configure Suite")
}
