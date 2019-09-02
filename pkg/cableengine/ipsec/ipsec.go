package ipsec

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/klog"
)

type engine struct {
	sync.Mutex

	driver        Driver
	localSubnets  []string
	localCluster  types.SubmarinerCluster
	localEndpoint types.SubmarinerEndpoint
}

func NewEngine(localSubnets []string, localCluster types.SubmarinerCluster, localEndpoint types.SubmarinerEndpoint) (cableengine.Engine, error) {
	driver, err := NewStrongSwan(localSubnets, localEndpoint)
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
