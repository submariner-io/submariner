package ipsec

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"

	"github.com/coreos/go-iptables/iptables"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/klog"
)

type engine struct {
	sync.Mutex

	driver        Driver
	localSubnets  []string
	localCluster  types.SubmarinerCluster
	localEndpoint types.SubmarinerEndpoint
}

func NewEngine(localSubnets []string, localCluster types.SubmarinerCluster, localEndpoint types.SubmarinerEndpoint) (cableengine.Engine, error) {
	driver, err := NewStrongSwan(localSubnets, localEndpoint)
	if err != nil {
		return nil, err
	}

	return &engine{
		localCluster:  localCluster,
		localEndpoint: localEndpoint,
		localSubnets:  localSubnets,
		driver:        driver,
	}, nil
}

func (i *engine) StartEngine() error {
	klog.Infof("Starting IPSec Engine (Charon)")
	ifi, err := util.GetDefaultGatewayInterface()
	if err != nil {
		return err
	}

	klog.V(8).Infof("Device of default gateway interface was %s", ifi.Name)
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error while initializing iptables: %v", err)
	}

	klog.V(6).Infof("Installing/ensuring the SUBMARINER-POSTROUTING and SUBMARINER-FORWARD chains")
	if err = ipt.NewChain("nat", "SUBMARINER-POSTROUTING"); err != nil {
		klog.Errorf("Unable to create SUBMARINER-POSTROUTING chain in iptables: %v", err)
	}

	if err = ipt.NewChain("filter", "SUBMARINER-FORWARD"); err != nil {
		klog.Errorf("Unable to create SUBMARINER-FORWARD chain in iptables: %v", err)
	}

	forwardToSubPostroutingRuleSpec := []string{"-j", "SUBMARINER-POSTROUTING"}
	if err = ipt.AppendUnique("nat", "POSTROUTING", forwardToSubPostroutingRuleSpec...); err != nil {
		klog.Errorf("Unable to append iptables rule \"%s\": %v\n", strings.Join(forwardToSubPostroutingRuleSpec, " "), err)
	}

	forwardToSubForwardRuleSpec := []string{"-j", "SUBMARINER-FORWARD"}
	rules, err := ipt.List("filter", "FORWARD")
	if err != nil {
		return fmt.Errorf("error listing the rules in FORWARD chain: %v", err)
	}

	appendAt := len(rules) + 1
	insertAt := appendAt
	for i, rule := range rules {
		if rule == "-A FORWARD -j SUBMARINER-FORWARD" {
			insertAt = -1
			break
		} else if rule == "-A FORWARD -j REJECT --reject-with icmp-host-prohibited" {
			insertAt = i
			break
		}
	}

	if insertAt == appendAt {
		// Append the rule at the end of FORWARD Chain.
		if err = ipt.Append("filter", "FORWARD", forwardToSubForwardRuleSpec...); err != nil {
			klog.Errorf("Unable to append iptables rule \"%s\": %v\n", strings.Join(forwardToSubForwardRuleSpec, " "), err)
		}
	} else if insertAt > 0 {
		// Insert the rule in the FORWARD Chain.
		if err = ipt.Insert("filter", "FORWARD", insertAt, forwardToSubForwardRuleSpec...); err != nil {
			klog.Errorf("Unable to insert iptables rule \"%s\" at position %d: %v\n", strings.Join(forwardToSubForwardRuleSpec, " "),
				insertAt, err)
		}
	}

	return i.driver.Init()
}

func (i *engine) InstallCable(endpoint types.SubmarinerEndpoint) error {
	if endpoint.Spec.ClusterID == i.localCluster.ID {
		klog.V(4).Infof("Not installing cable for local cluster")
		return nil
	}
	if reflect.DeepEqual(endpoint.Spec, i.localEndpoint.Spec) {
		klog.V(4).Infof("Not installing self")
		return nil
	}

	i.Lock()
	defer i.Unlock()

	klog.V(2).Infof("Installing cable %s", endpoint.Spec.CableName)
	activeConnections, err := i.driver.GetActiveConnections(endpoint.Spec.ClusterID)
	if err != nil {
		return err
	}
	for _, active := range activeConnections {
		klog.V(6).Infof("Analyzing currently active connection: %s", active)
		if active == endpoint.Spec.CableName {
			klog.V(6).Infof("Cable %s is already installed, not installing twice", active)
			return nil
		}
		if util.GetClusterIDFromCableName(active) == endpoint.Spec.ClusterID {
			return fmt.Errorf("error while installing cable %s, already found a pre-existing cable belonging to this cluster %s", active, endpoint.Spec.ClusterID)
		}
	}

	remoteEndpointIP, err := i.driver.ConnectToEndpoint(endpoint)
	if err != nil {
		return err
	}

	ifi, err := util.GetDefaultGatewayInterface()
	if err != nil {
		return err
	}
	klog.V(4).Infof("Device of default gateway interface was %s", ifi.Name)
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error while initializing iptables: %v", err)
	}

	addresses, err := ifi.Addrs()
	if err != nil {
		return err
	}

	for _, addr := range addresses {
		ipAddr, ipNet, err := net.ParseCIDR(addr.String())
		if err != nil {
			klog.Errorf("Error while parsing CIDR %s: %v", addr.String(), err)
			continue
		}

		if ipAddr.To4() != nil {
			for _, subnet := range endpoint.Spec.Subnets {
				ruleSpec := []string{"-s", ipNet.String(), "-d", subnet, "-i", ifi.Name, "-j", "ACCEPT"}
				klog.V(8).Infof("Installing iptables rule: %s", strings.Join(ruleSpec, " "))
				if err = ipt.AppendUnique("filter", "SUBMARINER-FORWARD", ruleSpec...); err != nil {
					klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
				}

				ruleSpec = []string{"-d", ipNet.String(), "-s", subnet, "-i", ifi.Name, "-j", "ACCEPT"}
				klog.V(8).Infof("Installing iptables rule: %v", ruleSpec)
				if err = ipt.AppendUnique("filter", "SUBMARINER-FORWARD", ruleSpec...); err != nil {
					klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
				}

				// -t nat -I POSTROUTING -s <local-network-cidr> -d <remote-cidr> -j SNAT --to-source <this-local-ip>
				ruleSpec = []string{"-s", ipNet.String(), "-d", subnet, "-j", "SNAT", "--to-source", ipAddr.String()}
				klog.V(8).Infof("Installing iptables rule: %s", strings.Join(ruleSpec, " "))
				if err = ipt.AppendUnique("nat", "SUBMARINER-POSTROUTING", ruleSpec...); err != nil {
					klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
				}
			}
		} else {
			klog.V(6).Infof("Skipping adding rule because IPv6 network %s found", ipNet.String())
		}
	}

	// MASQUERADE (on the GatewayNode) the incoming traffic from the remote cluster (i.e, remoteEndpointIP)
	// and destined to the local PODs (i.e., localSubnet) scheduled on the non-gateway node.
	// This will make the return traffic from the POD to go via the GatewayNode.
	for _, localSubnet := range i.localSubnets {
		ruleSpec := []string{"-s", remoteEndpointIP, "-d", localSubnet, "-j", "MASQUERADE"}
		klog.V(8).Infof("Installing iptables rule for MASQ incoming traffic: %v", ruleSpec)
		if err = ipt.AppendUnique("nat", "SUBMARINER-POSTROUTING", ruleSpec...); err != nil {
			klog.Errorf("error appending iptables MASQ rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	}

	return nil
}

func (i *engine) RemoveCable(cableID string) error {
	i.Lock()
	defer i.Unlock()

	return i.driver.DisconnectFromEndpoint(cableID)
}
