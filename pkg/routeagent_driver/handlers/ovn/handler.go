package ovn

import (
	"net"
	"sync"

	"github.com/pkg/errors"
	"k8s.io/klog"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	cableCleanup "github.com/submariner-io/submariner/pkg/cable/cleanup"
	"github.com/submariner-io/submariner/pkg/cable/wireguard"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/environment"
	"github.com/submariner-io/submariner/pkg/util"
)

type Handler struct {
	event.HandlerBase
	mutex                 sync.Mutex
	config                *environment.Specification
	smClient              clientset.Interface
	cableRoutingInterface *net.Interface
	cleanupHandlers       []cleanup.Handler
	localEndpoint         *submV1.Endpoint
	remoteEndpoints       map[string]*submV1.Endpoint
	isGateway             bool
	submarinerUpstreamIP  string
}

func NewHandler(env environment.Specification, smClientSet clientset.Interface) *Handler {
	return &Handler{
		config:          &env,
		smClient:        smClientSet,
		remoteEndpoints: map[string]*submV1.Endpoint{},
	}
}

func (ovn *Handler) GetName() string {
	return "ovn-hostroutes-handler"
}

func (ovn *Handler) GetNetworkPlugins() []string {
	return []string{"OVNKubernetes"}
}

func (ovn *Handler) Init() error {
	// For now we get all the cleanups
	ovn.cleanupHandlers = cableCleanup.GetCleanupHandlers()

	return ovn.initIPtablesChains()
}

func (ovn *Handler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	var routingInterface *net.Interface
	var err error

	// TODO: this logic belongs to the cabledrivers instead
	if endpoint.Spec.Backend == "wireguard" {
		//NOTE: This assumes that LocalEndpointCreated happens before than TransitionToGatewayNode
		if routingInterface, err = net.InterfaceByName(wireguard.DefaultDeviceName); err != nil {
			return errors.Wrapf(err, "Wireguard interface %s not found on the node.", wireguard.DefaultDeviceName)
		}
	} else {
		if routingInterface, err = util.GetDefaultGatewayInterface(); err != nil {
			klog.Fatalf("Unable to find the default interface on host: %s", err.Error())
		}
	}

	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.localEndpoint = endpoint
	ovn.cableRoutingInterface = routingInterface

	return nil
}

func (ovn *Handler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.localEndpoint = endpoint

	return nil
}

func (ovn *Handler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	if ovn.localEndpoint.Name == endpoint.Name {
		ovn.localEndpoint = nil
	}

	return nil
}

func (ovn *Handler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.remoteEndpoints[endpoint.Name] = endpoint

	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return errors.Wrapf(err, "updateHostNetworkDataplane returned error")
	}

	if ovn.isGateway {
		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.remoteEndpoints[endpoint.Name] = endpoint

	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return errors.Wrapf(err, "updateHostNetworkDataplane returned error")
	}

	if ovn.isGateway {
		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	delete(ovn.remoteEndpoints, endpoint.Name)

	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return errors.Wrapf(err, "updateHostNetworkDataplane returned error")
	}

	if ovn.isGateway {
		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) TransitionToNonGateway() error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.isGateway = false

	return ovn.cleanupGatewayDataplane()
}

func (ovn *Handler) TransitionToGateway() error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.isGateway = true

	return ovn.updateGatewayDataplane()
}
