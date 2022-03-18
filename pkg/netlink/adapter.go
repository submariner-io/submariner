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

// nolint:wrapcheck // Most of the functions are simple wrappers so we'll let the caller wrap errors.
package netlink

import (
	"net"
	"os"
	"syscall"

	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

type Adapter struct {
	Basic
}

func (a *Adapter) RuleAddIfNotPresent(rule *netlink.Rule) error {
	err := a.RuleAdd(rule)
	if err != nil && !os.IsExist(err) {
		return errors.Wrapf(err, "failed to add rule %s", rule)
	}

	return nil
}

func (a *Adapter) RouteAddOrReplace(route *netlink.Route) error {
	err := netlink.RouteAdd(route)

	if errors.Is(err, syscall.EEXIST) {
		err = netlink.RouteReplace(route)
	}

	return err
}

func (a *Adapter) AddDestinationRoutes(destIPs []net.IPNet, gwIP, srcIP net.IP, linkIndex, tableID int) error {
	for i := range destIPs {
		route := &netlink.Route{
			LinkIndex: linkIndex,
			Src:       srcIP,
			Dst:       &destIPs[i],
			Gw:        gwIP,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     tableID,
		}

		err := a.RouteAddOrReplace(route)
		if err != nil {
			return errors.Wrapf(err, "unable to add the route entry %#v", route)
		}
	}

	return nil
}
