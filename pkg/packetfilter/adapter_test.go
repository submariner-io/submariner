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

package packetfilter_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	fakePF "github.com/submariner-io/submariner/pkg/packetfilter/fake"
)

const chain = "test-chain"

var _ = Describe("Adapter", func() {
	var (
		pFilter *fakePF.PacketFilter
		adapter packetfilter.Interface
	)

	BeforeEach(func() {
		pFilter = fakePF.New()

		var err error

		adapter, err = packetfilter.New()
		Expect(err).To(Succeed())

		Expect(pFilter.CreateChainIfNotExists(packetfilter.TableTypeNAT, &packetfilter.Chain{
			Name: chain,
		})).To(Succeed())
	})

	Context("PrependUnique", func() {
		otherRule := &packetfilter.Rule{
			Action:      packetfilter.RuleActionJump,
			TargetChain: "other-chain",
		}

		rule1 := &packetfilter.Rule{
			Action:       packetfilter.RuleActionAccept,
			Proto:        packetfilter.RuleProtoUDP,
			OutInterface: "out-iface",
			InInterface:  "in-iface",
			DPort:        "1",
		}

		rule2 := &packetfilter.Rule{
			Action:    packetfilter.RuleActionMark,
			Proto:     packetfilter.RuleProtoTCP,
			MarkValue: "mark",
			SrcCIDR:   "171.254.1.0/24",
			DestCIDR:  "172.254.1.0/24",
		}

		rule3 := &packetfilter.Rule{
			Action:      packetfilter.RuleActionMss,
			Proto:       packetfilter.RuleProtoAll,
			ClampType:   packetfilter.ToPMTU,
			SrcSetName:  "src-set",
			DestSetName: "dest-set",
			MssValue:    "123",
		}

		rule4 := &packetfilter.Rule{
			Action:      packetfilter.RuleActionJump,
			Proto:       packetfilter.RuleProtoICMP,
			TargetChain: "target-chain",
		}

		rule5 := &packetfilter.Rule{
			Action:   packetfilter.RuleActionSNAT,
			Proto:    packetfilter.RuleProtoAll,
			SnatCIDR: "172.254.1.0/24",
		}

		rule6 := &packetfilter.Rule{
			Action:   packetfilter.RuleActionDNAT,
			Proto:    packetfilter.RuleProtoICMP,
			DnatCIDR: "173.254.1.0/24",
		}

		When("the rules don't exist", func() {
			It("should prepend them", func() {
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, otherRule)).To(Succeed())

				Expect(adapter.PrependUnique(packetfilter.TableTypeNAT, chain, rule1, rule2, rule3)).To(Succeed())
				assertRules(pFilter, rule1, rule2, rule3, otherRule)
			})
		})

		When("the rules already exist in the proper position", func() {
			It("should not prepend them", func() {
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule1)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule2)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule3)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, otherRule)).To(Succeed())

				Expect(adapter.PrependUnique(packetfilter.TableTypeNAT, chain, rule1, rule2, rule3)).To(Succeed())
				assertRules(pFilter, rule1, rule2, rule3, otherRule)
			})
		})

		When("rules already exist but not in the proper position", func() {
			It("should remove the misplaced rules and prepend them", func() {
				otherRule2 := &packetfilter.Rule{
					Action: packetfilter.RuleActionDNAT,
					Proto:  packetfilter.RuleProtoAll,
				}

				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule4)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, otherRule)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule1)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule2)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, otherRule2)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule3)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule1)).To(Succeed())

				Expect(adapter.PrependUnique(packetfilter.TableTypeNAT, chain, rule1, rule2, rule3, rule4, rule5, rule6)).To(Succeed())
				assertRules(pFilter, rule1, rule2, rule3, rule4, rule5, rule6, otherRule, otherRule2)
			})
		})
	})
})

func assertRules(pFilter packetfilter.Driver, expected ...*packetfilter.Rule) {
	actual, err := pFilter.List(packetfilter.TableTypeNAT, chain)
	Expect(err).To(Succeed())
	Expect(actual).To(Equal(expected))
}
