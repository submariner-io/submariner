package kp_iptables

import (
	"fmt"
	"os"

	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/routeagent-driver/cni_interface"
	"github.com/submariner-io/submariner/pkg/routeagent-driver/constants"
)

type SyncHandler struct {
	event.HandlerBase
	clusterID        string
	namespace        string
	localClusterCidr []string
	localServiceCidr []string

	hostname string
	cniIface *cni_interface.CniInterface
}

func NewSyncHandler(env constants.Specification) *SyncHandler {
	return &SyncHandler{
		clusterID:        env.ClusterID,
		namespace:        env.Namespace,
		localClusterCidr: env.ClusterCidr,
		localServiceCidr: env.ServiceCidr,
	}
}

func (kp *SyncHandler) GetName() string {
	return "kubeproxy-iptables-handler"
}

func (kp *SyncHandler) GetNetworkPlugin() string {
	return event.AnyNetworkPlugin
}

func (kp *SyncHandler) Init() error {
	var err error
	kp.hostname, err = os.Hostname()
	if err != nil {
		klog.Fatalf("unable to determine hostname: %v", err)
	}

	cniIface, err := cni_interface.Discover(kp.localClusterCidr[0])
	if err == nil {
		// Configure CNI Specific changes
		kp.cniIface = cniIface
		err := cni_interface.ConfigureRpFilter(kp.cniIface.Name)
		if err != nil {
			return fmt.Errorf("ConfigureRpFilter returned error. %v", err)
		}
	} else {
		klog.Errorf("DiscoverCNIInterface returned error %v", err)
	}

	// Create the necessary IPTable chains in the filter and nat tables.
	err = kp.createIPTableChains()
	if err != nil {
		return fmt.Errorf("createIPTableChains returned error. %v", err)
	}

	return nil
}
