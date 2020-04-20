package route

import (
	"fmt"
	"io/ioutil"
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/log"
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
				klog.V(log.DEBUG).Infof("Interface %q has %q address", iface.Name, ipAddr)
				address := net.ParseIP(ipAddr.String())

				// Verify that interface has an address from cluster CIDR
				if clusterNetwork.Contains(address) {
					klog.V(log.DEBUG).Infof("Found CNI Interface %q that has IP %q from ClusterCIDR %q",
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
		klog.V(log.DEBUG).Infof("Successfully configured rp_filter to loose mode(2) on cniInterface %q", iface)
	}
	return nil
}

func (r *Controller) annotateNodeWithCNIInterfaceIP(nodeName string) error {
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := r.clientSet.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to get node info for node %v, err: %s", nodeName, err)
		} else {
			annotations := node.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[CniInterfaceIp] = r.cniIface.ipAddress
			node.SetAnnotations(annotations)
			_, updateErr := r.clientSet.CoreV1().Nodes().Update(node)
			return updateErr
		}
	})

	if retryErr != nil {
		return fmt.Errorf("error updatating node %q, err: %s", nodeName, retryErr)
	}

	klog.Infof("Successfully annotated node %q with cniIfaceIP %q", nodeName, r.cniIface.ipAddress)
	return nil
}
