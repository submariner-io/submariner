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
package iptables

import (
	"fmt"
	"strings"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/iptables"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"

	"k8s.io/klog"
)

type Interface interface {
	AddClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error
	RemoveClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error
}

type ipTables struct {
	ipt iptables.Interface
}

func New() (Interface, error) {
	iptableHandler, err := iptables.New()
	if err != nil {
		return nil, err
	}

	iptableIface := &ipTables{
		ipt: iptableHandler,
	}

	return iptableIface, nil
}

func (i *ipTables) AddClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{"-p", "all", "-s", sourceIP, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	klog.V(log.DEBUG).Infof("Installing iptable egress rules for Cluster: %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetEgressChainForCluster, ruleSpec...); err != nil {
		return fmt.Errorf("error appending iptables rule \"%s\": %v", strings.Join(ruleSpec, " "), err)
	}

	return nil
}

func (i *ipTables) RemoveClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{"-p", "all", "-s", sourceIP, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	klog.V(log.DEBUG).Infof("Deleting iptable egress rules for Cluster: %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.Delete("nat", constants.SmGlobalnetEgressChainForCluster, ruleSpec...); err != nil {
		return fmt.Errorf("error deleting iptables rule \"%s\": %v", strings.Join(ruleSpec, " "), err)
	}

	return nil
}
