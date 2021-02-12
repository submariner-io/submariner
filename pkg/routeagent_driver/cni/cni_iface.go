/*
© 2021 Red Hat, Inc. and others

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
package cni

import (
	"fmt"
	"net"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

type Interface struct {
	Name      string
	IPAddress string
}

// DiscoverFunc is a hook for unit tests
var DiscoverFunc func(clusterCIDR string) (*Interface, error)

func Discover(clusterCIDR string) (*Interface, error) {
	if DiscoverFunc != nil {
		return DiscoverFunc(clusterCIDR)
	}

	return discover(clusterCIDR)
}

func discover(clusterCIDR string) (*Interface, error) {
	_, clusterNetwork, err := net.ParseCIDR(clusterCIDR)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to ParseCIDR %q", clusterCIDR)
	}

	hostInterfaces, err := net.Interfaces()
	if err != nil {
		return nil, errors.Wrapf(err, "net.Interfaces() returned error")
	}

	for _, iface := range hostInterfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, errors.Wrapf(err, "for interface %q, iface.Addrs returned error", iface.Name)
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
						iface.Name, ipAddr, clusterCIDR)
					return &Interface{IPAddress: ipAddr.String(), Name: iface.Name}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("unable to find CNI Interface on the host which has IP from %q", clusterCIDR)
}

func AnnotateNodeWithCNIInterfaceIP(nodeName string, clientSet kubernetes.Interface, clusterCidr []string) error {
	cniIface, err := Discover(clusterCidr[0])
	if err != nil {
		return fmt.Errorf("DiscoverCNIInterface returned error %v", err)
	}

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := clientSet.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to get node info for node %v, err: %s", nodeName, err)
		}

		annotations := node.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[constants.CniInterfaceIP] = cniIface.IPAddress
		node.SetAnnotations(annotations)
		_, updateErr := clientSet.CoreV1().Nodes().Update(node)
		return updateErr
	})

	if retryErr != nil {
		return fmt.Errorf("error updatating node %q, err: %s", nodeName, retryErr)
	}

	klog.Infof("Successfully annotated node %q with cniIfaceIP %q", nodeName, cniIface.IPAddress)

	return nil
}
