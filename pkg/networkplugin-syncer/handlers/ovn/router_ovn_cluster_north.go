package ovn

import (
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
)

func (ovn *SyncHandler) connectOvnClusterRouterToSubm() error {
	linkCmd, _ := ovn.nbdb.LinkSwitchToRouter(
		submarinerDownstreamSwitch, ovnClusterSubmarinerSwPort,
		ovnClusterRouter, ovnClusterSubmarinerRPort,
		ovnClusterSubmarinerMAC,
		[]string{ovnClusterSubmarinerNET}, nil,
	)

	err := ovn.nbdb.Execute(linkCmd)
	if err != nil {
		return errors.Wrapf(err, "Creating %q port %q", ovnClusterRouter, ovnClusterSubmarinerRPort)
	}

	return nil
}

func (ovn *SyncHandler) associateSubmarinerExternalPortToChassis(chassis *goovn.Chassis) error {
	if ovn.lastOvnGwChassis != chassis.Name {
		// TODO: make this less stateful, by listing the existing gateway chassis entries, and leaving only
		//       the desired state
		if ovn.lastOvnGwChassis != "" {
			err := ovn.nbctl.DelGatewayChassis(submarinerUpstreamRPort, chassis.Name, 0)
			if err != nil {
				return errors.Wrapf(err, "Error deleting the gateway chassis for %q", submarinerUpstreamRPort)
			}
		}

		err := ovn.nbctl.SetGatewayChassis(submarinerUpstreamRPort, chassis.Name, 0)
		if err != nil {
			return errors.Wrapf(err, "Error setting the new gateway chassis for %q", submarinerUpstreamRPort)
		}
	}

	return nil
}

func (ovn *SyncHandler) setupOvnClusterRouterRemoteRules() error {
	existingSubnetPolicies, err := ovn.nbctl.LrPolicyGetSubnets(ovnClusterRouter, submarinerDownstreamIP)
	if err != nil {
		return errors.Wrapf(err, "Reading existing routing policies from %q", ovnClusterRouter)
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
		err := ovn.nbctl.LrPolicyDel(ovnClusterRouter, 10, "ip4.dst == "+subnet)
		if err != nil {
			return errors.Wrapf(err, "Adding routing rule to router %q", ovnClusterRouter)
		}
	}

	return nil
}

func (ovn *SyncHandler) addPoliciesForRemoteSubnets(toAdd []string) error {
	for _, subnet := range toAdd {
		// add policy to ovn_cluster_router via submainer router 169.254.34.1
		err := ovn.nbctl.LrPolicyAdd(ovnClusterRouter, 10, "ip4.dst == "+subnet, "reroute", submarinerDownstreamIP)
		if err != nil {
			return errors.Wrapf(err, "Adding routing rule to router %q", ovnClusterRouter)
		}
	}

	return nil
}
