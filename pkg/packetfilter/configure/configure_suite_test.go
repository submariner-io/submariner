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
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/packetfilter/configure"
	"github.com/submariner-io/submariner/pkg/packetfilter/iptables"
	"github.com/submariner-io/submariner/pkg/packetfilter/nftables"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DriverType int

const (
	IPTables DriverType = iota
	NfTables
)

const defaultDriver = IPTables

var _ = Describe("DriverFromConfigMap", func() {
	var cm *corev1.ConfigMap

	BeforeEach(func() {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "config",
			},
			Data: map[string]string{},
		}
	})

	When(configure.UseNftablesKey+" key is present", func() {
		Context("and set to true", func() {
			It("should set the nftables driver", func() {
				cm.Data[configure.UseNftablesKey] = "true"
				Expect(configure.DriverFromConfigMap(cm)).To(Succeed())
				verifyDriverFn(NfTables)
			})
		})

		Context("and set to false", func() {
			It("should set the iptables driver", func() {
				cm.Data[configure.UseNftablesKey] = "false"
				Expect(configure.DriverFromConfigMap(cm)).To(Succeed())
				verifyDriverFn(IPTables)
			})
		})
	})

	When(configure.UseNftablesKey+" key is not present", func() {
		It("should set the default driver", func() {
			Expect(configure.DriverFromConfigMap(cm)).To(Succeed())
			verifyDriverFn(defaultDriver)
		})
	})

	When("the Data map is nil", func() {
		It("should set the default driver", func() {
			cm.Data = nil
			Expect(configure.DriverFromConfigMap(cm)).To(Succeed())
			verifyDriverFn(defaultDriver)
		})
	})

	When(configure.UseNftablesKey+" key value is invalid", func() {
		It("should return an error", func() {
			cm.Data[configure.UseNftablesKey] = "bogus"
			Expect(configure.DriverFromConfigMap(cm)).ToNot(Succeed())
		})
	})

	When("the ConfigMap is nil", func() {
		It("should set the default driver", func() {
			Expect(configure.DriverFromConfigMap(nil)).To(Succeed())
			verifyDriverFn(defaultDriver)
		})
	})
})

func verifyDriverFn(dType DriverType) {
	fnValue := func(v interface{}) string {
		return fmt.Sprintf("%v", v)
	}

	if dType == NfTables {
		Expect(fnValue(packetfilter.GetNewDriverFn())).To(Equal(fnValue(nftables.New)))
	} else {
		Expect(fnValue(packetfilter.GetNewDriverFn())).To(Equal(fnValue(iptables.New)))
	}
}

func TestConfigure(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Configure Suite")
}
