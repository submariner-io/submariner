package ovn

import (
	"fmt"
	"strings"

	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"
)

const (
	// The ovn_cluster_router submariner port connects to the submariner router
	ovnClusterSubmarinerRPort  = "ovn_cluster_subm_lrp"
	ovnClusterSubmarinerSwPort = "ovn_cluster_subm_lsp"
	ovnClusterSubmarinerMAC    = "00:60:2f:10:01:02"
	ovnClusterSubmarinerNET    = ovnClusterSubmarinerIP + "/29"
	ovnClusterSubmarinerIP     = "169.254.34.2"
	ovnRoutePoliciesPrio       = 10
)

func (ovn *SyncHandler) connectOvnClusterRouterToSubm() error {
	klog.Infof("Ensuring %q is connected to %q", submarinerLogicalRouter, ovnClusterRouter)
	// TODO: Improve this in goovn so we don't need to delete/recreate everytime, LinkSwitchToRouter
	//       does not provide good error handling and ignores many corner cases.
	delOldRouterPort, _ := ovn.nbdb.LRPDel(ovnClusterRouter, ovnClusterSubmarinerRPort)
	_ = ovn.nbdb.Execute(delOldRouterPort)
	delOldLSP, _ := ovn.nbdb.LSPDel(ovnClusterSubmarinerSwPort)
	_ = ovn.nbdb.Execute(delOldLSP)

	linkCmd, err := ovn.nbdb.LinkSwitchToRouter(
		submarinerDownstreamSwitch, ovnClusterSubmarinerSwPort,
		ovnClusterRouter, ovnClusterSubmarinerRPort,
		ovnClusterSubmarinerMAC,
		[]string{ovnClusterSubmarinerNET}, nil,
	)

	if err != nil && !errors.Is(err, goovn.ErrorExist) {
		return errors.Wrapf(err, "creating command to connect ovn_cluster_router")
	}

	err = ovn.nbdb.Execute(linkCmd)
	if err != nil {
		return errors.Wrapf(err, "error creating %q port %q", ovnClusterRouter, ovnClusterSubmarinerRPort)
	}

	return nil
}

// changeMgmtAllowRelatedACL modifies an ACL installed by ovn-kubernetes, which in it's default
//                           form will prevent ICMP fragment packets getting back to pods, precluding
//                           Path MTU discovery from working
func (ovn *SyncHandler) changeMgmtAllowRelatedACL(chassisSwitch string) error {
	mgmtPortIP, err := ovn.getMgmtPortIP(chassisSwitch)
	if err != nil {
		return err
	}

	aclList, err := ovn.nbdb.ACLList(chassisSwitch)
	if err != nil {
		return errors.Wrapf(err, "error getting ACL list for chassis switch: %q", chassisSwitch)
	}

	for _, acl := range aclList {
		// If the ofending ACL is found, we switch it to a more neutral form that will allow our ICMP packet
		if !(acl.Action == "allow-related" &&
			acl.Match == "ip4.src=="+mgmtPortIP &&
			acl.Direction == "to-lport" &&
			acl.Priority == 1001) {
			continue
		}

		klog.Infof("Fixing ovn-kubernetes host mgmt ACL in switch %q", chassisSwitch)

		delCmd, err := ovn.nbdb.ACLDel(chassisSwitch, acl.Direction, acl.Match, acl.Priority, nil)
		if err != nil {
			return errors.Wrapf(err, "error building ACLDel cmd for %#v", acl)
		}

		err = ovn.nbdb.Execute(delCmd)
		if err != nil {
			return errors.Wrapf(err, "error executing ACLDel for allow-related in switch %q", chassisSwitch)
		}

		addCmd, err := ovn.nbdb.ACLAdd(chassisSwitch, acl.Direction, acl.Match, "allow",
			acl.Priority, nil, false, "", "")
		if err != nil {
			return errors.Wrapf(err, "error creating ACLAdd cmd for %#v with allow", acl)
		}

		err = ovn.nbdb.Execute(addCmd)
		if err != nil {
			return errors.Wrapf(err, "error executing ACLDel & ACLAdd for allow-related in switch %q", chassisSwitch)
		}

		return nil
	}

	klog.V(log.DEBUG).Infof("ovn-kubernetes host mgmt ACL wasn't present as allow-related in switch %q", chassisSwitch)

	return nil
}

func (ovn *SyncHandler) getMgmtPortIP(chassisSwitch string) (string, error) {
	// NOTE: tried to grab k8s- constant from ovn-kubernetes but that pulls a lot of go mod dependencies
	// which conflict with ours
	port, err := ovn.nbdb.LSPGet("k8s-" + chassisSwitch)
	if err != nil {
		return "", errors.Wrapf(err, "Unable to find host k8s-port for chassis switch %q", chassisSwitch)
	}

	if len(port.Addresses) == 0 {
		return "", fmt.Errorf("port %q has no addresses, trying to configure ACL rules", port.Name)
	}

	macIP := strings.Split(port.Addresses[0], " ")
	if len(macIP) != 2 {
		return "", fmt.Errorf("unable to parse port %q addresses %q, trying to configure ACL rules", port.Name, port.Addresses[0])
	}

	return macIP[1], nil
}

func (ovn *SyncHandler) associateSubmarinerExternalPortToChassis(chassis *goovn.Chassis) error {
	if ovn.lastOvnGwChassis != chassis.Name {
		// TODO: make this less stateful, by listing the existing gateway chassis entries, and leaving only
		//       the desired state
		if ovn.lastOvnGwChassis != "" {
			err := ovn.nbctl.DelGatewayChassis(submarinerUpstreamRPort, chassis.Name, 0)
			if err != nil {
				return errors.Wrapf(err, "error deleting the gateway chassis for %q", submarinerUpstreamRPort)
			}
		}

		err := ovn.nbctl.SetGatewayChassis(submarinerUpstreamRPort, chassis.Name, 0)
		if err != nil {
			return errors.Wrapf(err, "error setting the new gateway chassis for %q", submarinerUpstreamRPort)
		}
	}

	return nil
}

func (ovn *SyncHandler) setupOvnClusterRouterRemoteRules() error {
	existingSubnetPolicies, err := ovn.nbctl.LrPolicyGetSubnets(ovnClusterRouter, submarinerDownstreamIP)
	if err != nil {
		return errors.Wrapf(err, "error reading existing routing policies from %q", ovnClusterRouter)
	}

	klog.V(log.DEBUG).Infof("Existing routing policies in %q router for subnets %v", ovnClusterRouter, existingSubnetPolicies.Elements())

	toAdd, toRemove := ovn.getNorthSubnetsToAddAndRemove(existingSubnetPolicies)

	ovn.logRoutingChanges("north policies", ovnClusterRouter, toAdd, toRemove)

	err = ovn.addPoliciesForRemoteSubnets(toAdd)
	if err != nil {
		return err
	}

	err = ovn.removePoliciesForRemoteSubnets(toRemove)
	if err != nil {
		return err
	}

	return nil
}

func (ovn *SyncHandler) removePoliciesForRemoteSubnets(toRemove []string) error {
	for _, subnet := range toRemove {
		err := ovn.nbctl.LrPolicyDel(ovnClusterRouter, ovnRoutePoliciesPrio, "ip4.dst == "+subnet)
		if err != nil {
			return errors.Wrapf(err, "error removing the %q routing rule to router %q", subnet, ovnClusterRouter)
		}
	}

	return nil
}

func (ovn *SyncHandler) addPoliciesForRemoteSubnets(toAdd []string) error {
	for _, subnet := range toAdd {
		// add policy to ovn_cluster_router via submainer router 169.254.34.1
		err := ovn.nbctl.LrPolicyAdd(ovnClusterRouter, ovnRoutePoliciesPrio, "ip4.dst == "+subnet, "reroute", submarinerDownstreamIP)
		if err != nil {
			return errors.Wrapf(err, "error adding the %q routing rule to router %q", subnet, ovnClusterRouter)
		}
	}

	return nil
}
