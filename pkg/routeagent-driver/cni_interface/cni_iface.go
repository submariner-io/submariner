package cni_interface

import (
	"fmt"
	"io/ioutil"
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
)

type CniInterface struct {
	Name      string
	IPAddress string
}

func Discover(clusterCIDR string) (*CniInterface, error) {
	_, clusterNetwork, err := net.ParseCIDR(clusterCIDR)
	if err != nil {
		return nil, fmt.Errorf("unable to ParseCIDR %q : %v", clusterCIDR, err)
	}

	hostInterfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("net.Interfaces() returned error : %v", err)
	}

	for _, iface := range hostInterfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("for interface %q, iface.Addrs returned error: %v", iface.Name, err)
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
					return &CniInterface{IPAddress: ipAddr.String(), Name: iface.Name}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("unable to find CNI Interface on the host which has IP from %q", clusterCIDR)
}

func ConfigureRpFilter(iface string) error {
	// We won't ever create rp_filter, and its permissions are 644
	// #nosec G306
	err := ioutil.WriteFile("/proc/sys/net/ipv4/conf/"+iface+"/rp_filter", []byte("2"), 0644)
	if err != nil {
		return fmt.Errorf("unable to update rp_filter for cni_interface %q, err: %s", iface, err)
	} else {
		klog.V(log.DEBUG).Infof("Successfully configured rp_filter to loose mode(2) on cniInterface %q", iface)
	}

	return nil
}

func AnnotateNodeWithCNIInterfaceIP(nodeName, cniAnnotation string, clientSet kubernetes.Interface, clusterCidr []string) error {
	cniIface, err := Discover(clusterCidr[0])
	if err != nil {
		return fmt.Errorf("DiscoverCNIInterface returned error %v", err)
	}

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := clientSet.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to get node info for node %v, err: %s", nodeName, err)
		} else {
			annotations := node.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[cniAnnotation] = cniIface.IPAddress
			node.SetAnnotations(annotations)
			_, updateErr := clientSet.CoreV1().Nodes().Update(node)
			return updateErr
		}
	})

	if retryErr != nil {
		return fmt.Errorf("error updatating node %q, err: %s", nodeName, retryErr)
	}

	klog.Infof("Successfully annotated node %q with cniIfaceIP %q", nodeName, cniIface.IPAddress)

	return nil
}
