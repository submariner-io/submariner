package ovn

import (
	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/stringset"
	"k8s.io/klog"
)

func (ovn *SyncHandler) getExistingSubmarinerRouterRoutesToPort(lrp string) (stringset.Interface, error) {
	subnetRouteObjs, err := ovn.nbdb.LRSRList(submarinerLogicalRouter)
	if err != nil {
		return nil, errors.Wrapf(err, "error reading existing routes from %q going via port %q", submarinerLogicalRouter, lrp)
	}

	existingRoutes := filterRouteSubnetsViaPort(subnetRouteObjs, lrp)

	return existingRoutes, nil
}

func filterRouteSubnetsViaPort(subnetRouteObjs []*goovn.LogicalRouterStaticRoute, lrp string) stringset.Interface {
	routeSubnets := stringset.New()

	for _, route := range subnetRouteObjs {
		if route.OutputPort != nil && *route.OutputPort == lrp {
			routeSubnets.Add(route.IPPrefix)
		}
	}

	return routeSubnets
}

func (ovn *SyncHandler) addSubmRoutesToSubnets(toAdd []string, viaPort, nextHop string,
	ovnCommands []*goovn.OvnCommand) ([]*goovn.OvnCommand, error) {
	for _, subnet := range toAdd {
		addCmd, err := ovn.nbdb.LRSRAdd(submarinerLogicalRouter, subnet, nextHop, &viaPort, nil, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "error creating LRSRAdd for router %q", submarinerLogicalRouter)
		}

		ovnCommands = append(ovnCommands, addCmd)
	}

	return ovnCommands, nil
}

func (ovn *SyncHandler) logRoutingChanges(kind, router string, toAdd, toRemove []string) {
	if len(toAdd) == 0 && len(toRemove) == 0 {
		klog.Infof("%s for %q are up to date", kind, router)
	} else {
		if len(toAdd) > 0 {
			klog.Infof("New %s to add to %q : %v", kind, router, toAdd)
		}
		if len(toRemove) > 0 {
			klog.Infof("Old %s to remove from %q : %v", kind, router, toRemove)
		}
	}
}
