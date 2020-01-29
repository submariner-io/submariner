package route

import (
	"fmt"
	"io/ioutil"
	"net"

	"github.com/vishvananda/netlink"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/util"
)

func interfaceExists(iface string) bool {
	_, err := netlink.LinkByName(iface)
	return err == nil
}

func discoverCNIInterface(clusterCIDR string) string {
	cniInterfaceList := []string{
		"weave",     // Weave
		"tun0",      // OpenShift
		"flannel.1", // Flannel
	}

	for _, iface := range cniInterfaceList {
		if interfaceExists(iface) {
			klog.Infof("Found CNI interface [%s] on the Host", iface)
			ipv4Addr, err := util.GetIPv4AddressOnInterface(iface)
			if err != nil {
				klog.Errorf("error reading IPv4 address on CNI interface [%s]: %v", iface, err)
				return ""
			}
			address := net.ParseIP(ipv4Addr)

			_, clusterNetwork, err := net.ParseCIDR(clusterCIDR)
			if err != nil {
				klog.Errorf("Unable to ParseCIDR [%s] : %v\n", clusterCIDR, err)
				return ""
			}

			// Verify that iface has an address from cluster CIDR
			if clusterNetwork.Contains(address) {
				return iface
			}
		}
	}
	return ""
}

func toggleCNISpecificConfiguration(iface string) error {
	err := ioutil.WriteFile("/proc/sys/net/ipv4/conf/"+iface+"/rp_filter", []byte("2"), 0644)
	if err != nil {
		return fmt.Errorf("unable to update rp_filter for "+iface+", err: %s", err)
	} else {
		klog.Info("Successfully configured rp_filter to loose mode(2) on " + iface)
	}
	return nil
}
