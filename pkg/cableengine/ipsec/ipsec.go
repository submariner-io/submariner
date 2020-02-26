package ipsec

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/log"
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
	return i.driver.Init()
}

func (i *engine) InstallCable(endpoint types.SubmarinerEndpoint) error {
	if endpoint.Spec.ClusterID == i.localCluster.ID {
		klog.V(log.DEBUG).Infof("Not installing cable for local cluster")
		return nil
	}

	if reflect.DeepEqual(endpoint.Spec, i.localEndpoint.Spec) {
		klog.V(log.DEBUG).Infof("Not installing cable for local endpoint")
		return nil
	}

	klog.Infof("Installing Endpoint cable %q", endpoint.Spec.CableName)

	i.Lock()
	defer i.Unlock()

	activeConnections, err := i.driver.GetActiveConnections(endpoint.Spec.ClusterID)
	if err != nil {
		return err
	}

	for _, active := range activeConnections {
		klog.V(log.TRACE).Infof("Analyzing currently active connection %q", active)
		if active == endpoint.Spec.CableName {
			klog.V(log.DEBUG).Infof("Cable %q is already installed - not installing again", active)
			return nil
		}

		if util.GetClusterIDFromCableName(active) == endpoint.Spec.ClusterID {
			return fmt.Errorf("found a pre-existing cable %q that belongs to this cluster %s", active, endpoint.Spec.ClusterID)
		}
	}

	remoteEndpointIP, err := i.driver.ConnectToEndpoint(endpoint)
	if err != nil {
		return err
	}

	klog.Infof("Successfully installed Endpoint cable %q with remote IP %s", endpoint.Spec.CableName, remoteEndpointIP)
	return nil
}

func (i *engine) RemoveCable(endpoint types.SubmarinerEndpoint) error {
	klog.Infof("Removing Endpoint cable %q", endpoint.Spec.CableName)

	i.Lock()
	defer i.Unlock()

	err := i.driver.DisconnectFromEndpoint(endpoint)
	if err != nil {
		return err
	}

	klog.Infof("Successfully removed Endpoint cable %q", endpoint.Spec.CableName)
	return nil
}
