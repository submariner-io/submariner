package route

import (
	"fmt"
	"io/ioutil"
	"net"

	"k8s.io/klog"
)

func discoverCNIInterface(clusterCIDR string) *cniInterface {
	_, clusterNetwork, err := net.ParseCIDR(clusterCIDR)
	if err != nil {
		klog.Errorf("Unable to ParseCIDR %q : %v", clusterCIDR, err)
		return nil
	}

	hostInterfaces, err := net.Interfaces()
	if err != nil {
		klog.Errorf("net.Interfaces() returned error : %v", err)
		return nil
	}

	for _, iface := range hostInterfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			klog.Errorf("For interface %q, iface.Addrs returned error: %v", iface.Name, err)
			return nil
		}

		for i := range addrs {
			ipAddr, _, err := net.ParseCIDR(addrs[i].String())
			if err != nil {
				klog.Errorf("Unable to ParseCIDR : %q", addrs[i].String())
			} else if ipAddr.To4() != nil {
				klog.V(4).Infof("Interface %q has %q address", iface.Name, ipAddr)
				address := net.ParseIP(ipAddr.String())

				// Verify that interface has an address from cluster CIDR
				if clusterNetwork.Contains(address) {
					klog.V(4).Infof("Found CNI Interface %q that has IP %q from ClusterCIDR %q",
						iface.Name, ipAddr.String(), clusterCIDR)
					return &cniInterface{ipAddress: ipAddr.String(), name: iface.Name}
				}
			}
		}
	}
	return nil
}

func toggleCNISpecificConfiguration(iface string) error {
	err := ioutil.WriteFile("/proc/sys/net/ipv4/conf/"+iface+"/rp_filter", []byte("2"), 0644)
	if err != nil {
		return fmt.Errorf("unable to update rp_filter for cni_interface %q, err: %s", iface, err)
	} else {
		klog.Infof("Successfully configured rp_filter to loose mode(2) on cniInterface %q", iface)
	}
	return nil
}
