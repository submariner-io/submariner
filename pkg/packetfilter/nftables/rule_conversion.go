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

package nftables

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

const serializedVersion = "1.0"

func protoToRuleSpec(ruleSpec []string, proto packetfilter.RuleProto, dPort string) []string {
	switch proto {
	case packetfilter.RuleProtoUDP:
		ruleSpec = append(ruleSpec, "ip", "protocol", "udp")
		if dPort != "" {
			ruleSpec = append(ruleSpec, "udp", "dport", dPort)
		}
	case packetfilter.RuleProtoTCP:
		ruleSpec = append(ruleSpec, "ip", "protocol", "tcp")
		if dPort != "" {
			ruleSpec = append(ruleSpec, "tcp", "dport", dPort)
		}
	case packetfilter.RuleProtoICMP:
		ruleSpec = append(ruleSpec, "ip", "protocol", "icmp")
	case packetfilter.RuleProtoAll:
	case packetfilter.RuleProtoUndefined:
	}

	return ruleSpec
}

func mssClampToRuleSpec(ruleSpec []string, clampType packetfilter.MssClampType, mssValue string) []string {
	switch clampType {
	case packetfilter.UndefinedMSS:
	case packetfilter.ToPMTU:
		ruleSpec = append(ruleSpec, "size", "set", "rt", "mtu")
	case packetfilter.ToValue:
		ruleSpec = append(ruleSpec, "size", "set", mssValue)
	}

	return ruleSpec
}

func setToRuleSpec(ruleSpec []string, srcSetName, destSetName string) []string {
	if srcSetName != "" {
		ruleSpec = append(ruleSpec, "ip", "saddr", "@"+srcSetName)
	}

	if destSetName != "" {
		ruleSpec = append(ruleSpec, "ip", "daddr", "@"+destSetName)
	}

	return ruleSpec
}

func toNftRuleSpec(rule *packetfilter.Rule) string {
	ruleSpec := protoToRuleSpec([]string{}, rule.Proto, rule.DPort)

	if rule.SrcCIDR != "" {
		ruleSpec = append(ruleSpec, "ip", "saddr", rule.SrcCIDR)
	}

	if rule.DestCIDR != "" {
		ruleSpec = append(ruleSpec, "ip", "daddr", rule.DestCIDR)
	}

	if rule.MarkValue != "" && rule.Action != packetfilter.RuleActionMark {
		// syntax for MarkValue '0xc0000': meta mark & 0xc0000 == 0xc0000
		ruleSpec = append(ruleSpec, "meta", "mark", "&", rule.MarkValue, "==", rule.MarkValue)
	}

	ruleSpec = setToRuleSpec(ruleSpec, rule.SrcSetName, rule.DestSetName)

	if rule.OutInterface != "" {
		ruleSpec = append(ruleSpec, "oifname", rule.OutInterface)
	}

	if rule.InInterface != "" {
		ruleSpec = append(ruleSpec, "iifname", rule.InInterface)
	}

	if rule.Action == packetfilter.RuleActionMss {
		ruleSpec = append(ruleSpec, "tcp", "flags", "syn / syn,rst")
	}

	ruleSpec = append(ruleSpec, "counter")
	ruleSpec = append(ruleSpec, ruleActionToStr[rule.Action]...)

	if rule.Action == packetfilter.RuleActionJump {
		ruleSpec = append(ruleSpec, rule.TargetChain)
	}

	if rule.SnatCIDR != "" {
		ruleSpec = append(ruleSpec, "to", rule.SnatCIDR)
	}

	if rule.DnatCIDR != "" {
		ruleSpec = append(ruleSpec, "to", rule.DnatCIDR)
	}

	ruleSpec = mssClampToRuleSpec(ruleSpec, rule.ClampType, rule.MssValue)

	if rule.MarkValue != "" && rule.Action == packetfilter.RuleActionMark {
		// syntax for MarkValue '0xc0000': set mark | 0xc0000
		ruleSpec = append(ruleSpec, "set", "mark", "|", rule.MarkValue)
	}

	str := strings.Join(ruleSpec, " ")

	logger.V(log.TRACE).Infof("toNftRuleSpec: from %q to %q", rule, str)

	return str
}

func writeString(buf *bytes.Buffer, s string) {
	// buf.WriteString documents that the return "err is always nil. If the buffer becomes too large, WriteString will
	// panic with [ErrTooLarge].". So we can safely ignore the return error.
	_, _ = buf.WriteString(s + " ")
}

func writeUint32(buf *bytes.Buffer, i uint32) {
	writeString(buf, strconv.FormatInt(int64(i), 10))
}

func SerializeRule(rule *packetfilter.Rule) string {
	var buf bytes.Buffer

	_, err := buf.WriteString(serializedVersion)
	utilruntime.Must(err)

	writeUint32(&buf, uint32(rule.Action))
	writeUint32(&buf, uint32(rule.ClampType))
	writeUint32(&buf, uint32(rule.Proto))

	writeString(&buf, rule.SrcSetName)
	writeString(&buf, rule.DestSetName)
	writeString(&buf, rule.SrcCIDR)
	writeString(&buf, rule.DestCIDR)
	writeString(&buf, rule.SnatCIDR)
	writeString(&buf, rule.DnatCIDR)
	writeString(&buf, rule.OutInterface)
	writeString(&buf, rule.InInterface)
	writeString(&buf, rule.DPort)
	writeString(&buf, rule.TargetChain)
	writeString(&buf, rule.MssValue)
	writeString(&buf, rule.MarkValue)

	str := buf.String()

	logger.V(log.TRACE).Infof("SerializeRule: from %q to %q (%d)", rule, str, len(str))

	return str
}

func mustReadString(buf *bytes.Buffer) string {
	b, err := buf.ReadBytes(' ')
	utilruntime.Must(err)

	return string(b[0 : len(b)-1])
}

func mustReadUint32(buf *bytes.Buffer) uint32 {
	s := mustReadString(buf)
	u, err := strconv.ParseUint(s, 10, 32)
	utilruntime.Must(err)

	return uint32(u)
}

func DeserializeRule(s string) (*packetfilter.Rule, error) {
	rule := &packetfilter.Rule{}

	buf := bytes.NewBufferString(s)

	b := make([]byte, 3)

	_, err := buf.Read(b)
	if err != nil {
		return nil, errors.Wrapf(err, "error deserializing rule %q", s)
	}

	version := string(b)
	if version != serializedVersion {
		return nil, errors.Errorf("unable to deserialize rule from %q: invalid version %q", s, version)
	}

	rule.Action = packetfilter.RuleAction(mustReadUint32(buf))
	rule.ClampType = packetfilter.MssClampType(mustReadUint32(buf))
	rule.Proto = packetfilter.RuleProto(mustReadUint32(buf))

	rule.SrcSetName = mustReadString(buf)
	rule.DestSetName = mustReadString(buf)
	rule.SrcCIDR = mustReadString(buf)
	rule.DestCIDR = mustReadString(buf)
	rule.SnatCIDR = mustReadString(buf)
	rule.DnatCIDR = mustReadString(buf)
	rule.OutInterface = mustReadString(buf)
	rule.InInterface = mustReadString(buf)
	rule.DPort = mustReadString(buf)
	rule.TargetChain = mustReadString(buf)
	rule.MssValue = mustReadString(buf)
	rule.MarkValue = mustReadString(buf)

	logger.V(log.TRACE).Infof("DeserializeRule: from %q to %q", s, rule)

	return rule, nil
}
