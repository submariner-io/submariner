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

const (
	chain        = "test-chain"
	targetChain1 = "target-chain1"
	targetChain2 = "target-chain2"
)

var _ = Describe("Adapter", func() {
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
		TargetChain: targetChain1,
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

	rule7 := &packetfilter.Rule{
		Action:    packetfilter.RuleActionMss,
		Proto:     packetfilter.RuleProtoUndefined,
		ClampType: packetfilter.ToValue,
		MssValue:  "456",
	}

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

		Expect(pFilter.CreateChainIfNotExists(packetfilter.TableTypeRoute, &packetfilter.Chain{
			Name: chain,
		})).To(Succeed())

		Expect(pFilter.CreateChainIfNotExists(packetfilter.TableTypeNAT, &packetfilter.Chain{
			Name: targetChain1,
		})).To(Succeed())

		Expect(pFilter.CreateChainIfNotExists(packetfilter.TableTypeRoute, &packetfilter.Chain{
			Name: targetChain1,
		})).To(Succeed())

		Expect(pFilter.CreateChainIfNotExists(packetfilter.TableTypeNAT, &packetfilter.Chain{
			Name: targetChain2,
		})).To(Succeed())
	})

	Context("PrependUnique", func() {
		otherRule := &packetfilter.Rule{
			Action:      packetfilter.RuleActionJump,
			TargetChain: targetChain2,
		}

		When("the rules don't exist", func() {
			It("should prepend them", func() {
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, otherRule)).To(Succeed())

				Expect(adapter.PrependUnique(packetfilter.TableTypeNAT, chain, rule1, rule2, rule3)).To(Succeed())
				assertRules(pFilter, packetfilter.TableTypeNAT, rule1, rule2, rule3, otherRule)
			})
		})

		When("the rules already exist in the proper position", func() {
			It("should not prepend them", func() {
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule1)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule2)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule3)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, otherRule)).To(Succeed())

				Expect(adapter.PrependUnique(packetfilter.TableTypeNAT, chain, rule1, rule2, rule3)).To(Succeed())
				assertRules(pFilter, packetfilter.TableTypeNAT, rule1, rule2, rule3, otherRule)
			})
		})

		When("rules already exist but not in the proper position", func() {
			It("should remove the misplaced rules and prepend them", func() {
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule4)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, otherRule)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule1)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule2)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule7)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule3)).To(Succeed())
				Expect(adapter.Append(packetfilter.TableTypeNAT, chain, rule1)).To(Succeed())

				Expect(adapter.PrependUnique(packetfilter.TableTypeNAT, chain, rule1, rule2, rule3, rule4, rule5, rule6)).To(Succeed())
				assertRules(pFilter, packetfilter.TableTypeNAT, rule1, rule2, rule3, rule4, rule5, rule6, otherRule, rule7)
			})
		})
	})

	Context("UpdateChainRules", func() {
		It("should correctly update the rules", func() {
			Expect(adapter.Append(packetfilter.TableTypeRoute, chain, rule1)).To(Succeed())
			Expect(adapter.Append(packetfilter.TableTypeRoute, chain, rule3)).To(Succeed())
			Expect(adapter.Append(packetfilter.TableTypeRoute, chain, rule5)).To(Succeed())
			Expect(adapter.Append(packetfilter.TableTypeRoute, chain, rule6)).To(Succeed())

			Expect(adapter.UpdateChainRules(packetfilter.TableTypeRoute, chain, []*packetfilter.Rule{
				rule2,
				rule3,
				rule4,
				rule5,
				rule7,
			})).To(Succeed())

			assertRules(pFilter, packetfilter.TableTypeRoute, rule3, rule5, rule2, rule4, rule7)
		})
	})
})

func assertRules(pFilter packetfilter.Driver, table packetfilter.TableType, expected ...*packetfilter.Rule) {
	actual, err := pFilter.List(table, chain)
	Expect(err).To(Succeed())
	Expect(actual).To(Equal(expected))
}
