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

package nbctl

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/stringset"
	"k8s.io/klog"
)

type NbCtl struct {
	clientKey        string
	clientCert       string
	ca               string
	connectionString string
}

func New(db, clientkey, clientcert, ca string) *NbCtl {
	return &NbCtl{
		connectionString: db,
		clientKey:        clientkey,
		clientCert:       clientcert,
		ca:               ca,
	}
}

func (n *NbCtl) nbctl(parameters ...string) (output string, err error) {
	dbConnection, err := expandConnectionStringIPs(n.connectionString)
	if err != nil {
		return "", err
	}

	allParameters := []string{fmt.Sprintf("--db=%s", dbConnection)}

	if n.clientCert != "" {
		allParameters = append(allParameters, []string{"-c", n.clientCert}...)
	}

	if n.clientKey != "" {
		allParameters = append(allParameters, []string{"-p", n.clientKey}...)
	}

	if n.ca != "" {
		allParameters = append(allParameters, []string{"-C", n.ca}...)
	}

	allParameters = append(allParameters, parameters...)

	cmd := exec.Command("/usr/bin/ovn-nbctl", allParameters...)

	klog.V(log.DEBUG).Infof("ovn-nbctl %v", allParameters)

	out, err := cmd.CombinedOutput()

	strOut := string(out)

	if err != nil {
		klog.Errorf("error running ovn-nbctl %+v, output:\n%s", err, strOut)
		return strOut, err
	}

	return strOut, err
}

func (n *NbCtl) SetRouterChassis(router, chassis string) error {
	_, err := n.nbctl("set", "Logical_Router", router, "options:chassis="+chassis)
	return err
}

func (n *NbCtl) SetGatewayChassis(lrp, chassis string, prio int) error {
	_, err := n.nbctl("lrp-set-gateway-chassis", lrp, chassis, strconv.Itoa(prio))
	return err
}

func (n *NbCtl) DelGatewayChassis(lrp, chassis string, prio int) error {
	_, err := n.nbctl("lrp-del-gateway-chassis", lrp, chassis, strconv.Itoa(prio))
	return err
}

func (n *NbCtl) GetGatewayChassis(lrp, chassis string) (string, error) {
	output, err := n.nbctl("lrp-get-gateway-chassis", lrp, chassis)
	return output, err
}

func (n *NbCtl) LrSetLoadBalancers(lrID string, lbIDs []string) error {
	loadBalancerSet := fmt.Sprintf("load_balancer=[%s]", strings.Join(lbIDs, ","))
	_, err := n.nbctl("set", "Logical_Router", lrID, loadBalancerSet)

	return err
}

func (n *NbCtl) LrPolicyAdd(logicalRouter string, prio int, filter string, actions ...string) error {
	allParameters := []string{"lr-policy-add", logicalRouter, strconv.Itoa(prio), filter}
	allParameters = append(allParameters, actions...)
	_, err := n.nbctl(allParameters...)

	return err
}

func (n *NbCtl) LrPolicyDel(logicalRouter string, prio int, filter string) error {
	_, err := n.nbctl("lr-policy-del", logicalRouter, strconv.Itoa(prio), filter)
	return err
}

func (n *NbCtl) LrPolicyGetSubnets(logicalRouter, rerouteIP string) (stringset.Interface, error) {
	output, err := n.nbctl("lr-policy-list", logicalRouter)
	if err != nil {
		return nil, errors.Wrapf(err, "error getting existing routing policies for router %q", logicalRouter)
	}

	return parseLrPolicyGetOutput(output, rerouteIP), nil
}

func parseLrPolicyGetOutput(output, rerouteIP string) stringset.Interface {
	// Example output:
	// $ ovn-nbctl lr-policy-list submariner_router
	// Routing Policies
	//        10                                 ip.src==1.1.1.1/32         reroute              169.254.34.1
	//        10                                 ip.src==1.1.1.2/32         reroute              169.254.34.1
	//
	subnets := stringset.New()
	// TODO: make this regex more generic in a global variable, so we avoid re-compiling the regex on each call
	r := regexp.MustCompile("ip4\\.dst == ([0-9\\.]+/[0-9]+)[\\s\\t]+reroute[\\s\\t]+" + rerouteIP)
	for _, match := range r.FindAllStringSubmatch(output, -1) {
		if len(match) == 2 {
			subnets.Add(match[1])
		}
	}

	return subnets
}

// expand strings in the form of
// ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9641 to
// ssl:<ip1>:9641,ssl:<ip2>:9641,ssl:<ip3>:9641
func expandConnectionStringIPs(db string) (string, error) {
	return expandConnectionStringIPsDetail(db, net.LookupIP)
}

func expandConnectionStringIPsDetail(db string, resolver func(host string) ([]net.IP, error)) (string, error) {
	parts := strings.Split(db, ":")
	if len(parts) != 3 {
		return db, nil
	}

	protocol := parts[0]
	host := parts[1]
	port := parts[2]
	ips, err := resolver(host)
	if err != nil {
		return "", errors.Wrapf(err, "error resolving %q", host)
	}

	connections := []string{}

	for _, ip := range ips {
		connections = append(connections, fmt.Sprintf("%s:%s:%s", protocol, ip.String(), port))
	}

	return strings.Join(connections, ","), nil
}
