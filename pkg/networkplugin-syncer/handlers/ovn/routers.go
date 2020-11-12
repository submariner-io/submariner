package ovn

import (
	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/util"
)

func (ovn *SyncHandler) getExistingSubmarinerRouterRoutesToPort(lrp string) (*util.StringSet, error) {
	subnetRouteObjs, err := ovn.nbdb.LRSRList(submarinerLogicalRouter)
	if err != nil {
		return nil, errors.Wrapf(err, "Reading existing routes from %q going via port %q", submarinerLogicalRouter, lrp)
	}

	existingRoutes := util.NewStringSet()

	for _, route := range subnetRouteObjs {
		if route.OutputPort != nil && *route.OutputPort == lrp {
			existingRoutes.Add(route.IPPrefix)
		}
	}

	return existingRoutes, nil
}

func (ovn *SyncHandler) addSubmRoutesToSubnets(toAdd []string, viaPort, nextHop string,
	ovnCommands []*goovn.OvnCommand) ([]*goovn.OvnCommand, error) {
	for _, subnet := range toAdd {
		addCmd, err := ovn.nbdb.LRSRAdd(submarinerLogicalRouter, subnet, nextHop, &viaPort, nil, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "Creating LRSRAdd for router %q", submarinerLogicalRouter)
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
			klog.Infof("New %s to    add   to %q : %v", kind, router, toAdd)
		}
		if len(toRemove) > 0 {
			klog.Infof("Old %s to remove from %q : %v", kind, router, toRemove)
		}
	}
}
