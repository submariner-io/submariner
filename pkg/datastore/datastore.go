package datastore

import (
	"context"

	"github.com/rancher/submariner/pkg/types"
)

/*
 * The datastore interface is used to implement central broker datastores so that
 * the datastoresyncer can facilitate the management of local CRDs
 */

type Datastore interface {
	// This gets all clusters that match the color code
	GetClusters(colorCodes []string) ([]types.SubmarinerCluster, error)

	// This gets a single cluster based on cluster ID and returns a v1.Cluster or error based on what it retrieves
	GetCluster(clusterID string) (*types.SubmarinerCluster, error)

	// This gets all endpoints for the given cluster ID
	GetEndpoints(clusterID string) ([]types.SubmarinerEndpoint, error)

	// This gets a single endpoint based on the cluster ID and cableName passed in
	GetEndpoint(clusterID string, cableName string) (*types.SubmarinerEndpoint, error)

	// Watches all clusters and calls the passed in function on cluster change
	WatchClusters(ctx context.Context, selfClusterID string, colorCodes []string,
		onClusterChange func(cluster *types.SubmarinerCluster, deleted bool) error) error

	// Performs a watch of all endpoints and calls the passed in function based on information
	WatchEndpoints(ctx context.Context, selfClusterID string, colorCodes []string,
		onEndpointChange func(endpoint *types.SubmarinerEndpoint, deleted bool) error) error

	// This should be called to set the local cluster information.
	SetCluster(cluster *types.SubmarinerCluster) error

	// This should only ever be called to set the endpoint of the local node.
	SetEndpoint(endpoint *types.SubmarinerEndpoint) error

	// This should be called to remove an endpoint from use
	RemoveEndpoint(clusterID, cableName string) error

	// This should be called to remove a cluster from use
	RemoveCluster(clusterID string) error
}
