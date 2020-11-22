package kp_iptables

import (
	"fmt"
	"net"
	"os"
	"sync"

	"k8s.io/klog"

	cableCleanup "github.com/submariner-io/submariner/pkg/cable/cleanup"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/event"
	globalnetCleanup "github.com/submariner-io/submariner/pkg/globalnet/cleanup"
	"github.com/submariner-io/submariner/pkg/routeagent-driver/cni_interface"
	"github.com/submariner-io/submariner/pkg/routeagent-driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent/cleanup"
	"github.com/submariner-io/submariner/pkg/util"
)

type SyncHandler struct {
	event.HandlerBase
	clusterID        string
	namespace        string
	localCableDriver string
	localClusterCidr []string
	localServiceCidr []string

	remoteSubnets    *util.StringSet
	remoteVTEPs      *util.StringSet
	routeCacheGWNode *util.StringSet

	syncHandlerMutex     *sync.Mutex
	isGatewayNode        bool
	wasGatewayPreviously bool

	vxlanDevice      *vxLanIface
	vxlanGwIP        *net.IP
	hostname         string
	cniIface         *cni_interface.CniInterface
	defaultHostIface *net.Interface

	smClientSet     clientset.Interface
	cleanupHandlers []cleanup.Handler
}

func NewSyncHandler(env constants.Specification, smClientSet clientset.Interface) *SyncHandler {
	return &SyncHandler{
		clusterID:            env.ClusterID,
		namespace:            env.Namespace,
		localClusterCidr:     env.ClusterCidr,
		localServiceCidr:     env.ServiceCidr,
		localCableDriver:     "",
		remoteSubnets:        util.NewStringSet(),
		remoteVTEPs:          util.NewStringSet(),
		routeCacheGWNode:     util.NewStringSet(),
		syncHandlerMutex:     &sync.Mutex{},
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

func (kp *SyncHandler) GetNetworkPlugin() string {
	return event.AnyNetworkPlugin
}

func (kp *SyncHandler) Init() error {
	var err error
	kp.hostname, err = os.Hostname()
	if err != nil {
		klog.Fatalf("unable to determine hostname: %v", err)
	}

	kp.defaultHostIface, err = util.GetDefaultGatewayInterface()
	if err != nil {
		klog.Fatalf("Unable to find the default interface on host: %s", err.Error())
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

	// For now we get all the cleanups
	kp.installCleanupHandlers(cableCleanup.GetCleanupHandlers())
	kp.installCleanupHandlers(globalnetCleanup.GetGlobalnetCleanupHandlers(kp.clusterID, kp.namespace, kp.smClientSet))

	return nil
}
