package cableengine

import (
	"fmt"

	"reflect"
	"sync"

	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/klog"
)

// Engine represents an implementation of some remote connectivity mechanism, such as
// a VPN gateway.
// An Engine cooperates with, and delegates work to, a cable.Driver for implementing
// a secure connection to remote clusters.
type Engine interface {
	// StartEngine performs any general set up work needed independent of any remote connections.
	StartEngine() error
	// InstallCable performs any set up work needed for connecting to given remote endpoint.
	// Once InstallCable completes, it should be possible to connect to remote
	// Pods or Services behind the given endpoint.
	InstallCable(remote types.SubmarinerEndpoint) error
	// RemoveCable disconnects the Engine from the given remote endpoint. Upon completion.
	// remote Pods and Service may not be accessible any more.
	RemoveCable(remote types.SubmarinerEndpoint) error
}

type engine struct {
	sync.Mutex
	driver        cable.Driver
	localSubnets  []string
	localCluster  types.SubmarinerCluster
	localEndpoint types.SubmarinerEndpoint
}

// NewEngine creates a new Engine for the local cluster
func NewEngine(localSubnets []string, localCluster types.SubmarinerCluster, localEndpoint types.SubmarinerEndpoint) (Engine, error) {
	driver, err := cable.NewDriver(localSubnets, localEndpoint)
	if err != nil {
		return nil, err
	}
	return &engine{
		localCluster:  localCluster,
		localEndpoint: localEndpoint,
		localSubnets:  localSubnets,
		driver:        driver,
	}, nil
}

func (i *engine) StartEngine() error {
	klog.Infof("Starting IPSec Engine (Charon)")
	return i.driver.Init()
}

func (i *engine) InstallCable(endpoint types.SubmarinerEndpoint) error {
	if endpoint.Spec.ClusterID == i.localCluster.ID {
		klog.V(4).Infof("Not installing cable for local cluster")
		return nil
	}
	if reflect.DeepEqual(endpoint.Spec, i.localEndpoint.Spec) {
		klog.V(4).Infof("Not installing self")
		return nil
	}

	i.Lock()
	defer i.Unlock()

	klog.V(2).Infof("Installing cable %s", endpoint.Spec.CableName)
	activeConnections, err := i.driver.GetActiveConnections(endpoint.Spec.ClusterID)
	if err != nil {
		return err
	}
	for _, active := range activeConnections {
		klog.V(6).Infof("Analyzing currently active connection: %s", active)
		if active == endpoint.Spec.CableName {
			klog.V(6).Infof("Cable %s is already installed, not installing twice", active)
			return nil
		}
		if util.GetClusterIDFromCableName(active) == endpoint.Spec.ClusterID {
			return fmt.Errorf("error while installing cable %s, already found a pre-existing cable belonging to this cluster %s", active, endpoint.Spec.ClusterID)
		}
	}

	remoteEndpointIP, err := i.driver.ConnectToEndpoint(endpoint)
	if err != nil {
		return err
	}
	klog.V(4).Infof("Connected to remoteEndpointIP %s", remoteEndpointIP)
	return nil
}

func (i *engine) RemoveCable(endpoint types.SubmarinerEndpoint) error {
	i.Lock()
	defer i.Unlock()

	return i.driver.DisconnectFromEndpoint(endpoint)
}
