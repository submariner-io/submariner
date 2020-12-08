package kubeproxy_iptables

import (
	"net"
	"os"
	"sync"

	"github.com/pkg/errors"
	"k8s.io/klog"

	cableCleanup "github.com/submariner-io/submariner/pkg/cable/cleanup"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cni_interface"
	"github.com/submariner-io/submariner/pkg/util"
)

type SyncHandler struct {
	event.HandlerBase
	localCableDriver string
	localClusterCidr []string
	localServiceCidr []string

	remoteSubnets    *util.StringSet
	remoteVTEPs      *util.StringSet
	routeCacheGWNode *util.StringSet

	syncHandlerMutex     sync.Mutex
	isGatewayNode        bool
	wasGatewayPreviously bool

	vxlanDevice      *vxLanIface
	vxlanGwIP        *net.IP
	hostname         string
	cniIface         *cni_interface.Interface
	defaultHostIface *net.Interface

	smClientSet     clientset.Interface
	cleanupHandlers []cleanup.Handler
}

func NewSyncHandler(localClusterCidr, localServiceCidr []string, smClientSet clientset.Interface) *SyncHandler {
	return &SyncHandler{
		localClusterCidr:     localClusterCidr,
		localServiceCidr:     localServiceCidr,
		localCableDriver:     "",
		remoteSubnets:        util.NewStringSet(),
		remoteVTEPs:          util.NewStringSet(),
		routeCacheGWNode:     util.NewStringSet(),
		isGatewayNode:        false,
		wasGatewayPreviously: false,
		vxlanDevice:          nil,
		vxlanGwIP:            nil,
		smClientSet:          smClientSet,
	}
}

func (kp *SyncHandler) GetName() string {
	return "kubeproxy-iptables-handler"
}

func (kp *SyncHandler) GetNetworkPlugins() []string {
	return []string{"generic", "canal-flannel", "weave-net", "OpenShiftSDN"}
}

func (kp *SyncHandler) Init() error {
	var err error
	kp.hostname, err = os.Hostname()
	if err != nil {
		return errors.Wrapf(err, "unable to determine hostname")
	}

	kp.defaultHostIface, err = util.GetDefaultGatewayInterface()
	if err != nil {
		return errors.Wrapf(err, "Unable to find the default interface on host: %s", kp.hostname)
	}

	cniIface, err := cni_interface.Discover(kp.localClusterCidr[0])
	if err == nil {
		// Configure CNI Specific changes
		kp.cniIface = cniIface
		err := cni_interface.ConfigureRpFilter(kp.cniIface.Name)
		if err != nil {
			return errors.Wrapf(err, "ConfigureRpFilter returned error")
		}
	} else {
		// This is not a fatal error. Hostnetworking to remote cluster support will be broken
		// but other use-cases can continue to work.
		klog.Errorf("Error discovering the CNI interface %v", err)
	}

	// Create the necessary IPTable chains in the filter and nat tables.
	err = kp.createIPTableChains()
	if err != nil {
		return errors.Wrapf(err, "createIPTableChains returned error")
	}

	// For now we get all the cleanups
	kp.installCleanupHandlers(cableCleanup.GetCleanupHandlers())

	return nil
}
