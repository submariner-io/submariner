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

package chains

import (
	"github.com/submariner-io/submariner/pkg/packetfilter"
)

const (
	// IPTable chains used by RouteAgent.

	SmPostRouting     = "SUBMARINER-POSTROUTING"
	SmPostRoutingMss  = "SUBMARINER-POSTROUTING-MSS"
	SmInput           = "SUBMARINER-INPUT"
	SmForward         = "SUBMARINER-FORWARD"
	SmForwardMSSClamp = "SUBMARINER-FWD-MSSCLAMP"
	SmSelfSnat        = "SUBMARINER-SELF-SNAT"
)

func NewPostRouting() *packetfilter.ChainIPHook {
	return &packetfilter.ChainIPHook{
		Name:     SmPostRouting,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityFirst,
	}
}

func NewForwarding() *packetfilter.ChainIPHook {
	return &packetfilter.ChainIPHook{
		Name:     SmForward,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookForward,
		Priority: packetfilter.ChainPriorityFirst,
	}
}

func NewForwardingMSSClamp() *packetfilter.ChainIPHook {
	return &packetfilter.ChainIPHook{
		Name:     SmForwardMSSClamp,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookForward,
		Priority: packetfilter.ChainPriorityFirst,
	}
}

func NewInput() *packetfilter.ChainIPHook {
	return &packetfilter.ChainIPHook{
		Name:     SmInput,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookInput,
		Priority: packetfilter.ChainPriorityLast,
		JumpRule: &packetfilter.Rule{
			Proto:       packetfilter.RuleProtoUDP,
			Action:      packetfilter.RuleActionJump,
			TargetChain: SmInput,
		},
	}
}

func NewSelfSnat() *packetfilter.ChainIPHook {
	return &packetfilter.ChainIPHook{
		Name:     SmSelfSnat,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityMiddle,
	}
}
