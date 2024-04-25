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

package iptables_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/packetfilter/iptables"
)

var _ = Describe("Rule conversion", func() {
	Specify("should correctly convert to and from a rule spec string", func() {
		// -m set --match-set src-set src -m set --match-set dest-set dst -j TCPMSS -p tcp -m tcp --tcp-flags SYN,RST SYN --clamp-mss-to-pmtu
		testRuleConversion(&packetfilter.Rule{
			SrcSetName:  "src-set",
			DestSetName: "dest-set",
			Action:      packetfilter.RuleActionMss,
			ClampType:   packetfilter.ToPMTU,
		})

		// -m set --match-set src-set src -m set --match-set dest-set dst -j TCPMSS -p tcp -m tcp --tcp-flags SYN,RST SYN --set-mss mss-value
		testRuleConversion(&packetfilter.Rule{
			SrcSetName:  "src-set",
			DestSetName: "dest-set",
			MssValue:    "mss-value",
			Action:      packetfilter.RuleActionMss,
			ClampType:   packetfilter.ToValue,
		})

		// -p udp -m udp -s 171.254.1.0/24 -d 170.254.1.0/24 -o out-iface -i in-iface --dport d-port -j ACCEPT
		testRuleConversion(&packetfilter.Rule{
			Proto:        packetfilter.RuleProtoUDP,
			DestCIDR:     "170.254.1.0/24",
			SrcCIDR:      "171.254.1.0/24",
			OutInterface: "out-iface",
			InInterface:  "in-iface",
			DPort:        "d-port",
			Action:       packetfilter.RuleActionAccept,
		})

		// -p all -m mark --mark 0xc0000/0xc0000 -m set --match-set src-set src -j SNAT --to-source 172.254.1.0/24
		testRuleConversion(&packetfilter.Rule{
			Proto:      packetfilter.RuleProtoAll,
			SrcSetName: "src-set",
			MarkValue:  "0xc0000",
			SnatCIDR:   "172.254.1.0/24",
			Action:     packetfilter.RuleActionSNAT,
		})

		// -p all -s 171.254.1.0/24 -m mark --mark 0xc0000/0xc0000 -j SNAT --to-source 172.254.1.0/24
		testRuleConversion(&packetfilter.Rule{
			Proto:     packetfilter.RuleProtoTCP,
			SrcCIDR:   "171.254.1.0/24",
			MarkValue: "0xc0000",
			SnatCIDR:  "172.254.1.0/24",
			Action:    packetfilter.RuleActionSNAT,
		})

		// -p icmp -d 171.254.1.0/24 -j DNAT --to-destination 172.254.1.0/24
		testRuleConversion(&packetfilter.Rule{
			Proto:    packetfilter.RuleProtoICMP,
			DestCIDR: "171.254.1.0/24",
			DnatCIDR: "172.254.1.0/24",
			Action:   packetfilter.RuleActionDNAT,
		})

		// -d 171.254.1.0/24 -j MARK --set-mark 0xc0000/0xc0000
		testRuleConversion(&packetfilter.Rule{
			DestCIDR:  "171.254.1.0/24",
			MarkValue: "0xc0000",
			Action:    packetfilter.RuleActionMark,
		})

		// -p udp -m udp -j target-chain
		testRuleConversion(&packetfilter.Rule{
			Proto:       packetfilter.RuleProtoUDP,
			TargetChain: "target-chain",
			Action:      packetfilter.RuleActionJump,
		})
	})
})

func testRuleConversion(rule *packetfilter.Rule) {
	spec := iptables.ToRuleSpec(rule)

	parsed := iptables.FromRuleSpec(spec)
	Expect(parsed).To(Equal(rule))
}
