package route

import (
	"fmt"
	"io/ioutil"

	"github.com/vishvananda/netlink"
	"k8s.io/klog"
)

const FlannelInterface = "flannel.1"

func isFlannelCNI() error {
	_, err := netlink.LinkByName(FlannelInterface)
	if err != nil {
		return fmt.Errorf("error retrieving link by name %s: %v", FlannelInterface, err)
	}

	// FlannelInterface exists, return nil
	return nil
}

func toggleCNISpecificConfiguration() error {
	// When using Flannel CNI, reverse path filtering has to be configured with loose mode on the FlannelInterface
	if isFlannelCNI() == nil {
		err := ioutil.WriteFile("/proc/sys/net/ipv4/conf/"+FlannelInterface+"/rp_filter", []byte("2"), 0644)
		if err != nil {
			return fmt.Errorf("unable to update rp_filter for "+FlannelInterface+", err: %s", err)
		} else {
			klog.Info("Successfully configured rp_filter to loose mode(2) on " + FlannelInterface)
		}
	}
	return nil
}
