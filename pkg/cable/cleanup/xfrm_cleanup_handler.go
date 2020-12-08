package cleanup

import (
	"fmt"
	"syscall"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
)

type XFRMCleanupHandler struct{}

func NewXFRMCleanupHandler() cleanup.Handler {
	return &XFRMCleanupHandler{}
}

func (xc *XFRMCleanupHandler) GetName() string {
	return "XFRM cleanup handler"
}

func (xc *XFRMCleanupHandler) NonGatewayCleanup() error {
	currentXfrmPolicyList, err := netlink.XfrmPolicyList(syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("Error retrieving current xfrm policies: %v", err)
	}

	if len(currentXfrmPolicyList) > 0 {
		klog.Infof("Cleaning up %d XFRM policies", len(currentXfrmPolicyList))
	}

	for i := range currentXfrmPolicyList {
		klog.V(log.DEBUG).Infof("Deleting XFRM policy %s", currentXfrmPolicyList[i])

		if err = netlink.XfrmPolicyDel(&currentXfrmPolicyList[i]); err != nil {
			return fmt.Errorf("Error Deleting XFRM policy %s: %v", currentXfrmPolicyList[i], err)
		}
	}

	return nil
}

func (xc *XFRMCleanupHandler) GatewayToNonGatewayTransition() error {
	return nil
}
