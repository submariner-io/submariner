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

package globalnetdataplane

import (
	"strings"

	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/ipset"
	"github.com/submariner-io/submariner/pkg/iptables"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"k8s.io/klog"
	utilexec "k8s.io/utils/exec"
)

func UninstallDataPath() {
	ipt, err := iptables.New()
	if err != nil {
		klog.Fatal(err)
	}

	natTableChains := []string{
		// The chains have to be deleted in a specific order.
		constants.SmGlobalnetEgressChainForCluster,
		constants.SmGlobalnetEgressChainForHeadlessSvcPods,
		constants.SmGlobalnetEgressChainForNamespace,
		constants.SmGlobalnetEgressChainForPods,
		constants.SmGlobalnetIngressChain,
		constants.SmGlobalnetMarkChain,
		constants.SmGlobalnetEgressChain,
	}

	for _, chain := range natTableChains {
		err = ipt.FlushIPTableChain(constants.NATTable, chain)
		if err != nil {
			// Just log an error as this is part of uninstallation.
			klog.Errorf("Error flushing iptables chain %q: %v", chain, err)
		}
	}

	err = ipt.FlushIPTableChain(constants.NATTable, routeAgent.SmPostRoutingChain)
	if err != nil {
		klog.Errorf("Error flushing iptables chain %q: %v", routeAgent.SmPostRoutingChain, err)
	}

	if err := ipt.DeleteIPTableRule(constants.NATTable, "PREROUTING", constants.SmGlobalnetIngressChain); err != nil {
		klog.Errorf("Error deleting iptables rule for %q in PREROUTING chain: %v\n", constants.SmGlobalnetIngressChain, err)
	}

	for _, chain := range natTableChains {
		err = ipt.DeleteIPTableChain(constants.NATTable, chain)
		if err != nil {
			klog.Errorf("Error deleting iptables chain %q: %v", chain, err)
		}
	}

	ipsetIface := ipset.New(utilexec.New())

	ipSetList, err := ipsetIface.ListSets()
	if err != nil {
		klog.Errorf("Error listing ipsets: %v", err)
	}

	for _, set := range ipSetList {
		if strings.HasPrefix(set, IPSetPrefix) {
			err = ipsetIface.DestroySet(set)
			if err != nil {
				klog.Errorf("Error destroying the ipset %q: %v", set, err)
			}
		}
	}
}
