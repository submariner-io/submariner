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

package netlink

import (
	"io/fs"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/vishvananda/netlink"
	k8snet "k8s.io/utils/net"
)

type Adapter struct {
	Basic
}

func (a *Adapter) RuleAddIfNotPresent(rule *netlink.Rule) error {
	err := a.RuleAdd(rule)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return errors.Wrapf(err, "failed to add rule %s", rule)
	}

	return nil
}

func (a *Adapter) RuleDelIfPresent(rule *netlink.Rule) error {
	err := a.RuleDel(rule)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errors.Wrapf(err, "failed to delete rule %s", rule)
	}

	return nil
}

func (a *Adapter) RouteAddOrReplace(route *netlink.Route) error {
	err := a.RouteAdd(route)

	if errors.Is(err, fs.ErrExist) {
		err = a.RouteReplace(route)
	}

	return err
}

func (a *Adapter) GetDefaultGatewayInterface(family k8snet.IPFamily) (NetworkInterface, error) {
	routes, err := a.RouteList(nil, family)
	if err != nil {
		return nil, err
	}

	for i := range routes {
		if (routes[i].Dst == nil || routes[i].Dst.String() == allZeroesFamilyAddress[family]) && routes[i].LinkIndex > 0 {
			return a.InterfaceByIndex(routes[i].LinkIndex)
		}
	}

	return nil, errors.Errorf("default gateway interface could not be determined from routes: %s", resource.ToJSON(routes))
}
