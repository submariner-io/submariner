/*
© 2021 Red Hat, Inc. and others

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
package iptables

import (
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/iptables"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util"
)

func InitSubmarinerPostRoutingChain(ipt iptables.Interface) error {
	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmPostRoutingChain)

	if err := util.CreateChainIfNotExists(ipt, "nat", constants.SmPostRoutingChain); err != nil {
		return errors.Wrapf(err, "unable to create %q chain in iptables", constants.SmPostRoutingChain)
	}

	klog.V(log.DEBUG).Infof("Insert %s rule that has rules for inter-cluster traffic", constants.SmPostRoutingChain)

	forwardToSubPostroutingRuleSpec := []string{"-j", constants.SmPostRoutingChain}

	if err := util.PrependUnique(ipt, "nat", "POSTROUTING", forwardToSubPostroutingRuleSpec); err != nil {
		return errors.Wrapf(err, "unable to insert iptable rule in NAT table, POSTROUTING chain")
	}

	return nil
}
