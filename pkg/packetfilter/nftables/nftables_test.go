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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/packetfilter/nftables"
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

func testRuleConversion(rule *packetfilter.Rule) {
	spec := nftables.ToRuleSpec(rule)
	parsed := nftables.FromRuleSpec(spec)

	// in nftables syntax protoAll represented by empty string
	if rule.Proto == packetfilter.RuleProtoAll {
		rule.Proto = packetfilter.RuleProtoUndefined
	}

	Expect(parsed).To(Equal(rule))
}
