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

package kubeproxy

import (
	goerrors "errors"
	"net"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
)

type vxLanAttributes struct {
	name     string
	vxlanID  int
	group    net.IP
	srcAddr  net.IP
	vtepPort int
	mtu      int
}

type vxLanIface struct {
	netLink netlinkAPI.Interface
	link    *netlink.Vxlan
}

func (kp *SyncHandler) newVxlanIface(attrs *vxLanAttributes) (*vxLanIface, error) {
	iface := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:  attrs.name,
			MTU:   attrs.mtu,
			Flags: net.FlagUp,
		},
		VxlanId: attrs.vxlanID,
		SrcAddr: attrs.srcAddr,
		Group:   attrs.group,
		Port:    attrs.vtepPort,
	}

	vxLANIface := &vxLanIface{
		netLink: kp.netLink,
		link:    iface,
	}

	kp.vxlanDevice = vxLANIface

	if err := kp.createOrUpdateVxLanIface(); err != nil {
		return nil, err
	}

	// ip link set $vxLANIface up
	if err := kp.netLink.LinkSetUp(kp.vxlanDevice.link); err != nil {
		return nil, errors.Wrap(err, "failed to bring up VxLAN interface")
	}

	return vxLANIface, nil
}

func (kp *SyncHandler) createOrUpdateVxLanIface() error {
	err := kp.netLink.LinkAdd(kp.vxlanDevice.link)
	if goerrors.Is(err, syscall.EEXIST) {
		// Get the properties of existing vxlan interface
		existing, err := kp.netLink.LinkByName(kp.vxlanDevice.link.Name)
		if err != nil {
			return errors.Wrap(err, "failed to retrieve link info")
		}

		if isVxlanConfigTheSame(kp.vxlanDevice.link, existing) {
			klog.V(log.DEBUG).Infof("VxLAN interface already exists with same configuration.")

			kp.vxlanDevice.link = existing.(*netlink.Vxlan)

			return nil
		}

		klog.V(log.DEBUG).Infof("Creating interface %s", VxLANIface)

		// Config does not match, delete the existing interface and re-create it.
		if err = kp.netLink.LinkDel(existing); err != nil {
			return errors.Wrap(err, "failed to delete the existing vxlan interface")
		}

		if err = kp.netLink.LinkAdd(kp.vxlanDevice.link); err != nil {
			return errors.Wrap(err, "failed to re-create the the vxlan interface")
		}
	} else if err != nil {
		return errors.Wrap(err, "failed to create the the vxlan interface")
	}

	return nil
}

func isVxlanConfigTheSame(newLink, currentLink netlink.Link) bool {
	required := newLink.(*netlink.Vxlan)
	existing := currentLink.(*netlink.Vxlan)

	if required.VxlanId != existing.VxlanId {
		klog.Errorf("VxlanId of existing interface (%d) does not match with required VxlanId (%d)", existing.VxlanId, required.VxlanId)
		return false
	}

	if len(required.Group) > 0 && len(existing.Group) > 0 && !required.Group.Equal(existing.Group) {
		klog.Errorf("Vxlan Group (%v) of existing interface does not match with required Group (%v)", existing.Group, required.Group)
		return false
	}

	if len(required.SrcAddr) > 0 && len(existing.SrcAddr) > 0 && !required.SrcAddr.Equal(existing.SrcAddr) {
		klog.Errorf("Vxlan SrcAddr (%v) of existing interface does not match with required SrcAddr (%v)", existing.SrcAddr, required.SrcAddr)
		return false
	}

	if required.Port > 0 && existing.Port > 0 && required.Port != existing.Port {
		klog.V(log.DEBUG).Infof("Vxlan Port (%d) of existing interface does not match with required Port (%d)", existing.Port, required.Port)
		return false
	}

	return true
}

func (iface *vxLanIface) configureIPAddress(ipAddress net.IP, mask net.IPMask) error {
	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipAddress,
		Mask: mask,
	}}

	// Update the local cache with new srcIP
	iface.link.SrcAddr = ipAddress

	err := iface.netLink.AddrAdd(iface.link, ipConfig)
	if goerrors.Is(err, syscall.EEXIST) {
		return nil
	} else if err != nil {
		return errors.Wrapf(err, "unable to configure address (%s) on vxlan interface (%s)", ipAddress, iface.link.Name)
	}

	return nil
}

func buildNeighbors(hwAddr string, linkIndex int, addresses ...string) ([]netlink.Neigh, error) {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid MAC Address (%s) supplied", hwAddr)
	}

	if len(addresses) == 0 {
		return nil, errors.Errorf("invalid ipAddresses (%v) supplied", addresses)
	}

	neighbors := []netlink.Neigh{}
	for _, address := range addresses {
		neighbors = append(neighbors, netlink.Neigh{
			LinkIndex:    linkIndex,
			Family:       unix.AF_BRIDGE,
			Flags:        netlink.NTF_SELF,
			Type:         netlink.NDA_DST,
			IP:           net.ParseIP(address),
			State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
			HardwareAddr: macAddr,
		})
	}

	return neighbors, nil
}

func (iface *vxLanIface) ListFDB() (map[string]netlink.Neigh, error) {
	rawFdbEntries, err := iface.netLink.NeighList(iface.link.Index, unix.AF_BRIDGE)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to list FDB entries for %v", VxLANIface)
	}

	fbdEntries := map[string]netlink.Neigh{}
	for _, fdbEntry := range rawFdbEntries { // nolint:gocritic // TODO MAG POC
		fbdEntries[fdbEntry.IP.String()] = fdbEntry
	}

	return fbdEntries, nil
}

func getVxlanVtepIPAddress(ipAddr string) (net.IP, error) {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		return nil, errors.Errorf("invalid ipAddr [%s]", ipAddr)
	}

	ipSlice[0] = strconv.Itoa(VxLANVTepNetworkPrefix)
	vxlanIP := net.ParseIP(strings.Join(ipSlice, "."))

	return vxlanIP, nil
}

func (kp *SyncHandler) updateVxLANInterface() error {
	ipAddr, err := kp.getHostIfaceIPAddress()
	if err != nil {
		return errors.Wrap(err, "unable to retrieve the IPv4 address on the Host")
	}

	vtepIP, err := getVxlanVtepIPAddress(ipAddr.String())
	if err != nil {
		return errors.Wrapf(err, "failed to derive the vxlan vtepIP for %s", ipAddr)
	}

	// Derive the MTU based on the default outgoing interface
	vxlanMtu := kp.defaultHostIface.MTU - VxLANOverhead
	var tunnelRemotes []string
	var subNode string

	// add FDB entries for the ends of the tunnel, for GW nodes it's the remote cluster
	// networks, for non-GW nodes it's all other GW node addresses
	if kp.isGatewayNode {
		// We only want to setup FDP entries on Gateway nodes for non-GW nodes
		tunnelRemotes = kp.gwIPs.Difference(kp.allNodeIPs)
		subNode = "Gateway"
	} else {
		tunnelRemotes = kp.gwIPs.Elements()
		subNode = "Non-Gateway"
	}

	attrs := &vxLanAttributes{
		name:     VxLANIface,
		vxlanID:  100,
		group:    nil,
		srcAddr:  nil,
		vtepPort: VxLANPort,
		mtu:      vxlanMtu,
	}

	// Only make the submariner-vxlan interface or update it in the in
	// memory cache a single time
	// on all nodes, then just reconcile the FDB entries on demand
	// otherwise update our in cache of it
	if kp.vxlanDevice == nil {
		kp.vxlanDevice, err = kp.newVxlanIface(attrs)
		if err != nil {
			return errors.Wrapf(err, "failed to create vxlan interface on %s Node", subNode)
		}

		err = kp.netLink.EnableLooseModeReversePathFilter(VxLANIface)
		if err != nil {
			return errors.Wrap(err, "error enabling loose mode")
		}

		klog.V(log.DEBUG).Infof("Successfully configured reverse path filter to loose mode on %q", VxLANIface)

		err = kp.vxlanDevice.configureIPAddress(vtepIP, net.CIDRMask(8, 32))
		if err != nil {
			return errors.Wrap(err, "failed to configure vxlan interface ipaddress on the Gateway Node")
		}
	}

	klog.V(log.DEBUG).Infof("Reconciling desired fdb entries %v on %q for a %s node", tunnelRemotes, VxLANIface, subNode)

	if err := kp.reconcileVxSubFdbEntries(tunnelRemotes...); err != nil {
		return errors.Wrapf(err, "failed to reconcile intra-cluster fdb entries for %s", VxLANIface)
	}

	return nil
}

func (kp *SyncHandler) reconcileVxSubFdbEntries(destinations ...string) error {
	existingEntries, err := kp.vxlanDevice.ListFDB()
	if err != nil {
		return errors.Wrap(err, "failed to list existing fdb entries")
	}

	klog.V(log.DEBUG).Infof("Existing fdb entries on vx-submariner: %v", existingEntries)

	destinationsToAdd := []string{}

	for _, address := range destinations {
		// FDB entry does not exist
		if _, ok := existingEntries[address]; !ok {
			destinationsToAdd = append(destinationsToAdd, address)
			continue
		}
		// FDB entry does exist and needs to be kept, remove from
		// existing entries
		delete(existingEntries, address)
	}

	// Delete stale entries, i.e whatever wasn't removed from existingEntities
	for _, neighbor := range existingEntries { // nolint:gocritic // TODO MAG POC
		err = kp.vxlanDevice.netLink.NeighDel(&neighbor) // nolint // TODO MAG POC
		if err != nil {
			return errors.Wrapf(err, "unable to delete the bridge fdb entry %v", neighbor)
		}

		klog.V(log.DEBUG).Infof("Successfully deleted the bridge fdb entry %v", neighbor)
	}

	if len(destinationsToAdd) == 0 {
		klog.Warningf("No new FDB entries to add")
		return nil
	}

	// Build new entries
	desiredEntries, err := buildNeighbors("00:00:00:00:00:00", kp.vxlanDevice.link.Index, destinationsToAdd...)
	if err != nil {
		return errors.Wrap(err, "failed to generate desired fbd entries")
	}

	for _, neighbor := range desiredEntries { // nolint:gocritic // TODO MAG POC
		err = kp.vxlanDevice.netLink.NeighAppend(&neighbor) // nolint // TODO MAG POC
		if err != nil {
			return errors.Wrapf(err, "unable to add the bridge fdb entry %v", neighbor)
		}

		klog.V(log.DEBUG).Infof("Successfully added the bridge fdb entry %v", neighbor)
	}

	return nil
}
