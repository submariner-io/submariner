package fake

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework/ginkgowrapper"
	"github.com/submariner-io/submariner/pkg/datastore"
	"github.com/submariner-io/submariner/pkg/types"
)

type Datastore struct {
	endpoints           map[string]interface{}
	setEndpoint         chan *types.SubmarinerEndpoint
	errOnSetEndpoint    error
	errOnSetCluster     error
	errOnRemoveEndpoint error
	setCluster          chan *types.SubmarinerCluster
	removeEndpoint      chan string
	watchEndpoints      chan datastore.OnEndpointChange
	watchClusters       chan datastore.OnClusterChange
}

func New() *Datastore {
	return &Datastore{
		endpoints:      map[string]interface{}{},
		setEndpoint:    make(chan *types.SubmarinerEndpoint, 100),
		setCluster:     make(chan *types.SubmarinerCluster, 100),
		removeEndpoint: make(chan string, 100),
		watchEndpoints: make(chan datastore.OnEndpointChange, 100),
		watchClusters:  make(chan datastore.OnClusterChange, 100),
	}
}

func (d *Datastore) GetClusters(colorCodes []string) ([]types.SubmarinerCluster, error) {
	return nil, errors.New("Not implemented")
}

func (d *Datastore) GetCluster(clusterID string) (*types.SubmarinerCluster, error) {
	return nil, errors.New("Not implemented")
}

func (d *Datastore) GetEndpoints(clusterID string) ([]types.SubmarinerEndpoint, error) {
	switch o := d.endpoints[clusterID].(type) {
	case []types.SubmarinerEndpoint:
		return o, nil
	case error:
		return nil, o
	}

	return []types.SubmarinerEndpoint{}, nil
}

func (d *Datastore) GetEndpoint(clusterID, cableName string) (*types.SubmarinerEndpoint, error) {
	return nil, errors.New("Not implemented")
}

func (d *Datastore) WatchClusters(ctx context.Context, selfClusterID string, icolorCodes []string,
	onClusterChange datastore.OnClusterChange) error {
	d.watchClusters <- onClusterChange
	return nil
}

func (d *Datastore) WatchEndpoints(ctx context.Context, selfClusterID string, colorCodes []string,
	onEndpointChange datastore.OnEndpointChange) error {
	d.watchEndpoints <- onEndpointChange
	return nil
}

func (d *Datastore) SetCluster(cluster *types.SubmarinerCluster) error {
	err := d.errOnSetCluster
	if err != nil {
		d.errOnSetCluster = nil
		return err
	}

	d.setCluster <- cluster

	return nil
}

func (d *Datastore) SetEndpoint(endpoint *types.SubmarinerEndpoint) error {
	err := d.errOnSetEndpoint
	if err != nil {
		d.errOnSetEndpoint = nil
		return err
	}

	d.setEndpoint <- endpoint

	return nil
}

func (d *Datastore) RemoveEndpoint(clusterID, cableName string) error {
	err := d.errOnRemoveEndpoint
	if err != nil {
		d.errOnRemoveEndpoint = nil
		return err
	}

	d.removeEndpoint <- makeCableStr(clusterID, cableName)

	return nil
}

func (d *Datastore) RemoveCluster(clusterID string) error {
	return nil
}

func (d *Datastore) SetupGetEndpoints(clusterID string, err error, endpoints ...types.SubmarinerEndpoint) {
	if err != nil {
		d.endpoints[clusterID] = err
	} else {
		d.endpoints[clusterID] = endpoints
	}
}

func (d *Datastore) SetupErrOnFirstSetEndpoint(err error) {
	d.errOnSetEndpoint = err
}

func (d *Datastore) SetupErrOnFirstRemoveEndpoint(err error) {
	d.errOnRemoveEndpoint = err
}

func (d *Datastore) VerifySetEndpoint(expected *types.SubmarinerEndpoint) {
	Eventually(d.setEndpoint, 5).Should(Receive(Equal(expected)), "Endpoint was not set")
}

func (d *Datastore) VerifyNoSetEndpoint() {
	Consistently(d.setEndpoint, 300*time.Millisecond).ShouldNot(Receive(), "Endpoint was unexpectedly received")
}

func (d *Datastore) SetupErrOnFirstSetCluster() error {
	d.errOnSetCluster = errors.New("mock SetCluster error")
	return d.errOnSetCluster
}

func (d *Datastore) VerifySetCluster(expected *types.SubmarinerCluster) {
	Eventually(d.setCluster, 5).Should(Receive(Equal(expected)), "Cluster was not set")
}

func (d *Datastore) VerifyNoSetCluster() {
	Consistently(d.setCluster, 300*time.Millisecond).ShouldNot(Receive(), "Cluster was unexpectedly received")
}

func (d *Datastore) VerifyRemoveEndpoint(clusterID, cableName string) {
	Eventually(d.removeEndpoint, 5).Should(Receive(Equal(makeCableStr(clusterID, cableName))), "Endpoint was not removed")
}

func (d *Datastore) VerifyWatchClusters() datastore.OnClusterChange {
	select {
	case f := <-d.watchClusters:
		return f
	case <-time.After(5 * time.Second):
		ginkgowrapper.Fail("WatchClusters was not invoked")
		return nil
	}
}

func (d *Datastore) VerifyWatchEndpoints() datastore.OnEndpointChange {
	select {
	case f := <-d.watchEndpoints:
		return f
	case <-time.After(5 * time.Second):
		ginkgowrapper.Fail("WatchEndpoints was not invoked")
		return nil
	}
}

func makeCableStr(clusterID, cableName string) string {
	return fmt.Sprintf("clusterID: %s, cableName: %s", clusterID, cableName)
}
