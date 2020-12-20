package ovn

import (
	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"
)

const (
	// The upstream port connects to the host on the GW, right before/after the encryption cable driver
	submarinerUpstreamSwPort = "submariner_up_lsp"
	submarinerUpstreamRPort  = "submariner_up_lrp"
	submarinerUpstreamMAC    = "00:60:2f:10:01:01"
	submarinerUpstreamNET    = SubmarinerUpstreamIP + "/24"
	SubmarinerUpstreamIP     = "169.254.33.7" // public constant, used in the route-agent handler
	hostUpstreamIP           = "169.254.33.1"
	// the implementation IPs changed at some point
	// https://github.com/ovn-org/ovn-kubernetes/blob/master/go-controller/pkg/types/const.go#L50
	hostUpstreamIPv2        = "169.254.0.1"
	submarinerUpstreamNETv2 = SubmarinerUpstreamIPv2 + "/20"
	SubmarinerUpstreamIPv2  = "169.254.0.7"
)

func (ovn *SyncHandler) createOrUpdateSubmarinerExternalPort(extLogicalSwitch string) error {
	// TODO: Improve this so we don't need to delete/recreate everytime
	delOldRouterPort, _ := ovn.nbdb.LRPDel(submarinerLogicalRouter, submarinerUpstreamRPort)
	_ = ovn.nbdb.Execute(delOldRouterPort)
	delOldLSP, _ := ovn.nbdb.LSPDel(submarinerUpstreamSwPort)
	_ = ovn.nbdb.Execute(delOldLSP)

	linkCmd, err := ovn.nbdb.LinkSwitchToRouter(
		extLogicalSwitch, submarinerUpstreamSwPort,
		submarinerLogicalRouter, submarinerUpstreamRPort,
		submarinerUpstreamMAC,
		[]string{ovn.submarinerUpstreamNet}, nil,
	)

	if err == nil {
		klog.Infof("Creating submariner upstream port %q", submarinerUpstreamRPort)

		err = ovn.nbdb.Execute(linkCmd)
		if err != nil {
			return errors.Wrapf(err, "error creating the submariner upstream port %q", submarinerUpstreamRPort)
		}
	} else {
		return errors.Wrapf(err, "error creating the submariner upstream port command %q", submarinerUpstreamRPort)
	}

	return nil
}

func (ovn *SyncHandler) updateSubmarinerRouterRemoteRoutes() error {
	existingRoutes, err := ovn.getExistingSubmarinerRouterRoutesToPort(submarinerUpstreamRPort)
	if err != nil {
		return err
	}

	klog.V(log.DEBUG).Infof("Existing north routes in %q router for subnets %v", submarinerLogicalRouter, existingRoutes.Elements())

	toAdd, toRemove := ovn.getNorthSubnetsToAddAndRemove(existingRoutes)

	ovn.logRoutingChanges("north routes", submarinerLogicalRouter, toAdd, toRemove)

	ovnCommands, err := ovn.addSubmRoutesToSubnets(toAdd, submarinerUpstreamRPort, ovn.hostUpstreamIP, []*goovn.OvnCommand{})
	if err != nil {
		return err
	}

	ovnCommands, err = ovn.removeRoutesToSubnets(toRemove, submarinerUpstreamRPort, ovnCommands)
	if err != nil {
		return err
	}

	err = ovn.nbdb.Execute(ovnCommands...)
	if err != nil {
		return errors.Wrapf(err, "error executing routing rule modifications for router %q", submarinerLogicalRouter)
	}

	return nil
}

func (ovn *SyncHandler) removeRoutesToSubnets(toRemove []string, viaPort string, ovnCommands []*goovn.OvnCommand) ([]*goovn.OvnCommand,
	error) {
	for _, subnet := range toRemove {
		delCmd, err := ovn.nbdb.LRSRDel(submarinerLogicalRouter, subnet, nil, &viaPort, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "error creating LRSRDel for router %q", submarinerLogicalRouter)
		}

		ovnCommands = append(ovnCommands, delCmd)
	}

	return ovnCommands, nil
}
