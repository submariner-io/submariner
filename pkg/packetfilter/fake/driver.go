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

package fake

import (
	"net"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	k8snet "k8s.io/utils/net"
)

type driverImpl struct {
	pfilter *PacketFilter
	family  k8snet.IPFamily
}

func (d *driverImpl) ChainExists(table packetfilter.TableType, chain string) (bool, error) {
	return d.pfilter.ChainExists(table, chain)
}

func (d *driverImpl) CreateIPHookChainIfNotExists(chain *packetfilter.ChainIPHook) error {
	return d.pfilter.CreateIPHookChainIfNotExists(chain)
}

func (d *driverImpl) CreateChainIfNotExists(table packetfilter.TableType, chain *packetfilter.Chain) error {
	return d.pfilter.CreateChainIfNotExists(table, chain)
}

func (d *driverImpl) DeleteIPHookChain(chain *packetfilter.ChainIPHook) error {
	return d.pfilter.DeleteIPHookChain(chain)
}

func (d *driverImpl) DeleteChain(table packetfilter.TableType, chain string) error {
	return d.pfilter.DeleteChain(table, chain)
}

func (d *driverImpl) ClearChain(table packetfilter.TableType, chain string) error {
	return d.pfilter.ClearChain(table, chain)
}

func (d *driverImpl) List(table packetfilter.TableType, chain string) ([]*packetfilter.Rule, error) {
	return d.pfilter.List(table, chain)
}

func (d *driverImpl) Delete(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	if err := d.verifyRule(rule); err != nil {
		return err
	}

	return d.pfilter.Delete(table, chain, rule)
}

func (d *driverImpl) AppendUnique(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	if err := d.verifyRule(rule); err != nil {
		return err
	}

	return d.pfilter.AppendUnique(table, chain, rule)
}

func (d *driverImpl) Append(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	if err := d.verifyRule(rule); err != nil {
		return err
	}

	return d.pfilter.Append(table, chain, rule)
}

func (d *driverImpl) Insert(table packetfilter.TableType, chain string, pos int, rule *packetfilter.Rule) error {
	if err := d.verifyRule(rule); err != nil {
		return err
	}

	return d.pfilter.Insert(table, chain, pos, rule)
}

func (d *driverImpl) NewNamedSet(set *packetfilter.SetInfo) packetfilter.NamedSet {
	return d.pfilter.NewNamedSet(set, d.family)
}

func (d *driverImpl) DestroySets(nameFilter func(string) bool) error {
	return d.pfilter.DestroySets(nameFilter)
}

func (d *driverImpl) verifyRule(rule *packetfilter.Rule) error {
	var errs []error

	return utilerrors.NewAggregate(append(errs, verifyFamilyOf(rule.SrcCIDR, "SrcCIDR", d.family),
		verifyFamilyOf(rule.DestCIDR, "DestCIDR", d.family),
		verifyFamilyOf(rule.SnatCIDR, "SnatCIDR", d.family),
		verifyFamilyOf(rule.DnatCIDR, "DnatCIDR", d.family)))
}

func verifyFamilyOf(s, name string, expectedFamily k8snet.IPFamily) error {
	if s == "" {
		return nil
	}

	ipRange := strings.Split(s, "-")
	if len(ipRange) == 2 {
		return utilerrors.NewAggregate([]error{
			verifyFamilyOf(ipRange[0], name, expectedFamily),
			verifyFamilyOf(ipRange[1], name, expectedFamily),
		})
	}

	var family k8snet.IPFamily

	if ip := net.ParseIP(s); ip != nil {
		family = k8snet.IPFamilyOf(ip)
	} else {
		family = k8snet.IPFamilyOfCIDRString(s)
	}

	if family == expectedFamily {
		return nil
	}

	return errors.Errorf("the family of %q (IPv%v) for %q is not allowed", s, family, name)
}
