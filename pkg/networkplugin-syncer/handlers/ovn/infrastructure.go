package ovn

import (
	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"
	"k8s.io/klog"
)

const (
	submarinerLogicalRouter = "submariner_router"
	ovnClusterRouter        = "ovn_cluster_router"
)

func (ovn *SyncHandler) ensureSubmarinerInfra() error {
	if err := ovn.ensureSubmarinerJoinSwitch(); err != nil {
		return err
	}

	if err := ovn.ensureSubmarinerRouter(); err != nil {
		return err
	}

	if err := ovn.connectOvnClusterRouterToSubm(); err != nil {
		return err
	}

	// At this point, we are missing the ovn_cluster_router policies and the
	// local-to-remote & remote-to-local routes in submariner_router, that
	// depends on endpoint details that we will receive via events

	return nil
}

func (ovn *SyncHandler) ensureSubmarinerJoinSwitch() error {
	lsCmd, err := ovn.nbdb.LSAdd(submarinerDownstreamSwitch)
	if err == nil {
		klog.Infof("Creating submariner switch %q", submarinerDownstreamSwitch)

		err = ovn.nbdb.Execute(lsCmd)
	}

	if !errors.Is(err, goovn.ErrorExist) {
		return errors.Wrapf(err, "Creating submariner switch %q", submarinerDownstreamSwitch)
	}

	return nil
}

func (ovn *SyncHandler) ensureSubmarinerRouter() error {
	lrCmd, err := ovn.nbdb.LRAdd(submarinerLogicalRouter, nil)
	if err == nil {
		klog.Infof("Creating submariner router %q", submarinerLogicalRouter)

		err = ovn.nbdb.Execute(lrCmd)
	}

	if !errors.Is(err, goovn.ErrorExist) {
		return errors.Wrapf(err, "Creating submariner router %q", submarinerLogicalRouter)
	}

	// TODO: Improve this so we don't need to delete/recreate everytime
	delOldRouterPort, _ := ovn.nbdb.LRPDel(submarinerLogicalRouter, submarinerDownstreamRPort)
	_ = ovn.nbdb.Execute(delOldRouterPort)
	delOldLSP, _ := ovn.nbdb.LSPDel(submarinerDownstreamSwPort)
	_ = ovn.nbdb.Execute(delOldLSP)

	linkCmd, _ := ovn.nbdb.LinkSwitchToRouter(
		submarinerDownstreamSwitch, submarinerDownstreamSwPort,
		submarinerLogicalRouter, submarinerDownstreamRPort,
		submarinerDownstreamMAC,
		[]string{submarinerDownstreamNET}, nil,
	)

	err = ovn.nbdb.Execute(linkCmd)
	if err != nil {
		return errors.Wrapf(err, "Creating %q port %q", submarinerLogicalRouter, submarinerDownstreamRPort)
	}

	return nil
}
