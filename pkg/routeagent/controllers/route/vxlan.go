package route

import (
	"net"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
)

type vxLanAttributes struct {
	name     string
	vxlanId  int
	group    net.IP
	srcAddr  net.IP
	vtepPort int
	mtu      int
}

type vxLanIface struct {
	link *netlink.Vxlan
}

func newVxlanIface(attrs *vxLanAttributes) (*vxLanIface, error) {
	iface := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:  attrs.name,
			MTU:   attrs.mtu,
			Flags: net.FlagUp,
		},
		VxlanId: attrs.vxlanId,
		SrcAddr: attrs.srcAddr,
		Group:   attrs.group,
		Port:    attrs.vtepPort,
	}

	vxLANIface := &vxLanIface{
		link: iface,
	}

	if err := createVxLanIface(vxLANIface); err != nil {
		return nil, err
	}

	return vxLANIface, nil
}

func createVxLanIface(iface *vxLanIface) error {
	err := netlink.LinkAdd(iface.link)
	if err == syscall.EEXIST {
		// Get the properties of existing vxlan interface
		existing, err := netlink.LinkByName(iface.link.Name)
		if err != nil {
			return err
		}

		if isVxlanConfigTheSame(iface.link, existing) {
			klog.V(4).Infof("VxLAN interface already exists with same configuration.")
			iface.link = existing.(*netlink.Vxlan)
			return nil
		}

		// Config does not match, delete the existing interface and re-create it.
		if err = netlink.LinkDel(existing); err != nil {
			klog.V(4).Infof("Failed to delete the existing vxlan interface: %v", err)
			return err
		}

		if err = netlink.LinkAdd(iface.link); err != nil {
			klog.V(4).Infof("Failed to re-create the the vxlan interface: %v", err)
			return err
		}
	} else if err != nil {
		klog.V(4).Infof("Failed to create the the vxlan interface: %v", err)
		return err
	}

	return nil
}

func isVxlanConfigTheSame(new, current netlink.Link) bool {

	required := new.(*netlink.Vxlan)
	existing := current.(*netlink.Vxlan)

	if required.VxlanId != existing.VxlanId {
		klog.V(4).Infof("VxlanId of existing interface (%d) does not match with required VxlanId (%d)", existing.VxlanId, required.VxlanId)
		return false
	}

	if len(required.Group) > 0 && len(existing.Group) > 0 && !required.Group.Equal(existing.Group) {
		klog.V(4).Infof("Vxlan Group of existing interface (%v) does not match with required Group (%v)", existing.Group, required.Group)
		return false
	}

	if len(required.SrcAddr) > 0 && len(existing.SrcAddr) > 0 && !required.SrcAddr.Equal(existing.SrcAddr) {
		klog.V(4).Infof("Vxlan SrcAddr of existing interface (%v) does not match with required SrcAddr (%v)", existing.SrcAddr, required.SrcAddr)
		return false
	}

	if required.Port > 0 && existing.Port > 0 && required.Port != existing.Port {
		klog.V(4).Infof("Vxlan Port of existing interface (%d) does not match with required Port (%d)", existing.Port, required.Port)
		return false
	}

	return true
}

func (iface *vxLanIface) configureIPAddress(ipAddress net.IP, mask net.IPMask) error {
	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipAddress,
		Mask: mask,
	}}

	err := netlink.AddrAdd(iface.link, ipConfig)
	if err == syscall.EEXIST {
		return nil
	} else if err != nil {
		klog.Errorf("Unable to configure address (%s) on vxlan interface (%s). %v", ipAddress, iface.link.Name, err)
		return err
	}
	return nil
}

func (iface *vxLanIface) AddFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		klog.Errorf("Invalid MAC Address (%s) supplied. %v", hwAddr, err)
		return err
	}

	neigh := &netlink.Neigh{
		LinkIndex:    iface.link.Index,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		Type:         netlink.NDA_DST,
		IP:           ipAddress,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		HardwareAddr: macAddr,
	}

	err = netlink.NeighAppend(neigh)
	if err != nil {
		klog.Errorf("Unable to add the bridge fdb entry %v, err: %s", neigh, err)
		return err
	} else {
		klog.V(4).Infof("Successfully added the bridge fdb entry %v", neigh)
	}
	return nil
}

func (iface *vxLanIface) DelFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		klog.Errorf("Invalid MAC Address (%s) supplied. %v", hwAddr, err)
		return err
	}

	neigh := &netlink.Neigh{
		LinkIndex:    iface.link.Index,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		Type:         netlink.NDA_DST,
		IP:           ipAddress,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		HardwareAddr: macAddr,
	}

	err = netlink.NeighDel(neigh)
	if err != nil {
		klog.Errorf("Unable to delete the bridge fdb entry %v, err: %s", neigh, err)
		return err
	} else {
		klog.V(4).Infof("Successfully deleted the bridge fdb entry %v", neigh)
	}
	return nil
}
