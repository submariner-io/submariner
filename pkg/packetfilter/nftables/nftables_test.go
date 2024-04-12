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

package nftables_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/packetfilter/nftables"
	"sigs.k8s.io/knftables"
)

var _ = Describe("Rule conversion", func() {
	Specify("should correctly convert to and from a rule spec string", func() {
		// ip saddr @src-set ip daddr @dest-set tcp flags syn / syn,rst counter tcp option maxseg size set rt mtu
		testRuleConversion(&packetfilter.Rule{
			SrcSetName:  "src-set",
			DestSetName: "dest-set",
			Action:      packetfilter.RuleActionMss,
			ClampType:   packetfilter.ToPMTU,
		})

		// ip saddr @src-set ip daddr @dest-set tcp flags syn / syn,rst counter tcp option maxseg size set mss-value
		testRuleConversion(&packetfilter.Rule{
			SrcSetName:  "src-set",
			DestSetName: "dest-set",
			MssValue:    "mss-value",
			Action:      packetfilter.RuleActionMss,
			ClampType:   packetfilter.ToValue,
		})

		// iifname "in-iface" oifname "out-iface" ip saddr 171.254.1.0/24 ip daddr 170.254.1.0/24 udp dport d-port counter accept'
		testRuleConversion(&packetfilter.Rule{
			Proto:        packetfilter.RuleProtoUDP,
			DestCIDR:     "170.254.1.0/24",
			SrcCIDR:      "171.254.1.0/24",
			OutInterface: "out-iface",
			InInterface:  "in-iface",
			DPort:        "d-port",
			Action:       packetfilter.RuleActionAccept,
		})

		// mark mark-value ip saddr @src-set counter snat to 172.254.1.0/24
		testRuleConversion(&packetfilter.Rule{
			Proto:      packetfilter.RuleProtoAll,
			SrcSetName: "src-set",
			MarkValue:  "mark-value",
			SnatCIDR:   "172.254.1.0/24",
			Action:     packetfilter.RuleActionSNAT,
		})

		// ip saddr 171.254.1.0/24 mark mark-value counter snat to 172.254.1.0/24
		testRuleConversion(&packetfilter.Rule{
			Proto:     packetfilter.RuleProtoTCP,
			SrcCIDR:   "171.254.1.0/24",
			MarkValue: "mark-value",
			SnatCIDR:  "172.254.1.0/24",
			Action:    packetfilter.RuleActionSNAT,
		})

		// ip protocol icmp ip daddr 171.254.1.0/24 counter dnat to 172.254.1.0/24
		testRuleConversion(&packetfilter.Rule{
			Proto:    packetfilter.RuleProtoICMP,
			DestCIDR: "171.254.1.0/24",
			DnatCIDR: "172.254.1.0/24",
			Action:   packetfilter.RuleActionDNAT,
		})

		// ip daddr 171.254.1.0/24 counter meta mark set mark-value
		testRuleConversion(&packetfilter.Rule{
			DestCIDR:  "171.254.1.0/24",
			MarkValue: "mark-value",
			Action:    packetfilter.RuleActionMark,
		})

		// ip protocol udp counter jump target-chain
		testRuleConversion(&packetfilter.Rule{
			Proto:       packetfilter.RuleProtoUDP,
			TargetChain: "target-chain",
			Action:      packetfilter.RuleActionJump,
		})
	})
})

var _ = Describe("Interface", func() {
	const (
		chainName = "egress"
		setName   = "my-set"
	)

	var (
		fakeKnftables *fakeKnftablesWrapper
		pf            packetfilter.Driver

		setInfo = &packetfilter.SetInfo{
			Name:   setName,
			Table:  packetfilter.TableTypeNAT,
			Family: packetfilter.SetFamilyV4,
		}
	)

	BeforeEach(func() {
		fakeKnftables = &fakeKnftablesWrapper{knftables.NewFake(knftables.IPv4Family, "submariner")}
		pf = nftables.NewWithNft(fakeKnftables)
	})

	assertRules := func(r ...*packetfilter.Rule) {
		rules, err := pf.List(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())

		if len(r) == 0 {
			Expect(rules).To(BeEmpty())
		} else {
			Expect(rules).To(Equal(r))
		}
	}

	assertSets := func(s ...string) {
		sets, err := fakeKnftables.List(context.TODO(), "set")
		Expect(err).To(Succeed())

		if len(s) == 0 {
			Expect(sets).To(BeEmpty())
		} else {
			Expect(sets).To(Equal(s))
		}
	}

	assertEntries := func(set packetfilter.NamedSet, e ...string) {
		entries, err := set.ListEntries()
		Expect(err).To(Succeed())

		if len(e) == 0 {
			Expect(entries).To(BeEmpty())
		} else {
			Expect(entries).To(Equal(e))
		}
	}

	Specify("Creating and deleting a chain", func() {
		err := pf.CreateChainIfNotExists(packetfilter.TableTypeNAT, &packetfilter.Chain{
			Name: chainName,
		})
		Expect(err).To(Succeed())

		exists, err := pf.ChainExists(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())
		Expect(exists).To(BeTrue())

		// Already exists - should succeed.
		err = pf.CreateChainIfNotExists(packetfilter.TableTypeNAT, &packetfilter.Chain{
			Name: chainName,
		})
		Expect(err).To(Succeed())

		err = pf.DeleteChain(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())

		exists, err = pf.ChainExists(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())
		Expect(exists).To(BeFalse())

		// After deletion, these should be a no-op.
		err = pf.DeleteChain(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())

		err = pf.ClearChain(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())
	})

	Specify("Creating and deleting an IP hook chain", func() {
		chainIPHook := &packetfilter.ChainIPHook{
			Name:     chainName,
			Type:     packetfilter.ChainTypeNAT,
			Hook:     packetfilter.ChainHookPrerouting,
			Priority: packetfilter.ChainPriorityFirst,
		}

		err := pf.CreateIPHookChainIfNotExists(chainIPHook)
		Expect(err).To(Succeed())

		exists, err := pf.ChainExists(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())
		Expect(exists).To(BeTrue())

		// Already exists - should succeed.
		err = pf.CreateIPHookChainIfNotExists(chainIPHook)
		Expect(err).To(Succeed())

		err = pf.DeleteIPHookChain(chainIPHook)
		Expect(err).To(Succeed())

		exists, err = pf.ChainExists(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())
		Expect(exists).To(BeFalse())

		// After deletion, these should be a no-op.
		err = pf.DeleteIPHookChain(chainIPHook)
		Expect(err).To(Succeed())

		err = pf.ClearChain(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())
	})

	Specify("Adding and deleting rules", func() {
		err := pf.CreateChainIfNotExists(packetfilter.TableTypeNAT, &packetfilter.Chain{
			Name: chainName,
		})
		Expect(err).To(Succeed())

		By("Append the first rule")

		rule1 := &packetfilter.Rule{
			Proto:    packetfilter.RuleProtoICMP,
			DestCIDR: "171.254.1.0/24",
			DnatCIDR: "172.254.1.0/24",
			Action:   packetfilter.RuleActionDNAT,
		}

		err = pf.Append(packetfilter.TableTypeNAT, chainName, rule1)
		Expect(err).To(Succeed())

		assertRules(rule1)

		By("Prepend the second rule")

		rule2 := &packetfilter.Rule{
			Proto:    packetfilter.RuleProtoUDP,
			DestCIDR: "170.254.1.0/24",
			SrcCIDR:  "171.254.1.0/24",
			DPort:    "d-port",
			Action:   packetfilter.RuleActionAccept,
		}

		err = pf.Insert(packetfilter.TableTypeNAT, chainName, 1, rule2)
		Expect(err).To(Succeed())

		assertRules(rule2, rule1)

		By("Insert the third rule")

		rule3 := &packetfilter.Rule{
			Proto:    packetfilter.RuleProtoTCP,
			DestCIDR: "190.254.1.0/24",
			SrcCIDR:  "191.254.1.0/24",
			DPort:    "d-port",
			Action:   packetfilter.RuleActionAccept,
		}

		err = pf.Insert(packetfilter.TableTypeNAT, chainName, 2, rule3)
		Expect(err).To(Succeed())

		assertRules(rule2, rule3, rule1)

		By("Append unique the fourth rule")

		rule4 := &packetfilter.Rule{
			Proto:    packetfilter.RuleProtoICMP,
			DestCIDR: "161.254.1.0/24",
			SrcCIDR:  "161.254.1.0/24",
			Action:   packetfilter.RuleActionAccept,
		}

		err = pf.AppendUnique(packetfilter.TableTypeNAT, chainName, rule4)
		Expect(err).To(Succeed())

		assertRules(rule2, rule3, rule1, rule4)

		// Rule already exists - shouldn't append.
		err = pf.AppendUnique(packetfilter.TableTypeNAT, chainName, rule3)
		Expect(err).To(Succeed())

		assertRules(rule2, rule3, rule1, rule4)

		By("Delete some rules")

		err = pf.Delete(packetfilter.TableTypeNAT, chainName, rule1)
		Expect(err).To(Succeed())

		assertRules(rule2, rule3, rule4)

		// Try to delete again - should succeed.
		err = pf.Delete(packetfilter.TableTypeNAT, chainName, rule1)
		Expect(err).To(Succeed())

		err = pf.Delete(packetfilter.TableTypeNAT, chainName, rule2)
		Expect(err).To(Succeed())

		assertRules(rule3, rule4)

		By("Clear the chain")

		err = pf.ClearChain(packetfilter.TableTypeNAT, chainName)
		Expect(err).To(Succeed())

		assertRules()
	})

	Specify("Creating and deleting sets", func() {
		set := pf.NewNamedSet(setInfo)

		err := set.Create(true)
		Expect(err).To(Succeed())

		assertSets(set.Name())

		err = set.Destroy()
		Expect(err).To(Succeed())

		assertSets()

		Expect(set.Destroy()).To(Succeed())
		Expect(set.Flush()).To(Succeed())

		err = set.Create(true)
		Expect(err).To(Succeed())

		assertSets(set.Name())

		err = pf.DestroySets(func(s string) bool {
			return s == setName
		})
		Expect(err).To(Succeed())

		assertSets()
	})

	Specify("Adding and deleting entries from a set", func() {
		set := pf.NewNamedSet(setInfo)

		err := set.Create(true)
		Expect(err).To(Succeed())

		err = set.AddEntry("entry1", false)
		Expect(err).To(Succeed())
		assertEntries(set, "entry1")

		err = set.AddEntry("entry2", false)
		Expect(err).To(Succeed())
		assertEntries(set, "entry1", "entry2")

		err = set.DelEntry("entry1")
		Expect(err).To(Succeed())
		assertEntries(set, "entry2")

		err = set.Flush()
		Expect(err).To(Succeed())
		assertEntries(set)
	})
})

func testRuleConversion(rule *packetfilter.Rule) {
	spec := nftables.ToRuleSpec(rule)
	parsed := nftables.FromRuleSpec(spec)

	// in nftables syntax protoAll represented by empty string
	if rule.Proto == packetfilter.RuleProtoAll {
		rule.Proto = packetfilter.RuleProtoUndefined
	}

	Expect(parsed).To(Equal(rule))
}

type fakeKnftablesWrapper struct {
	*knftables.Fake
}

func (f *fakeKnftablesWrapper) ListRules(ctx context.Context, chain string) ([]*knftables.Rule, error) {
	rules, err := f.Fake.ListRules(ctx, chain)

	// The docs for ListRules interface says "the Rule objects will have their Comment and Handle fields filled in,
	// but not the actual Rule field.". However, the fake implementation doesn't honor this so clear out the Rule field.
	newRules := make([]*knftables.Rule, len(rules))

	for i := range rules {
		nr := *rules[i]
		nr.Rule = ""
		newRules[i] = &nr
	}

	return newRules, err
}
