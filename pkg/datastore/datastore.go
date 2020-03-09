package datastore

import (
	"context"

	"github.com/submariner-io/submariner/pkg/types"
)

/*
 * The datastore interface is used to implement central broker datastores so that
 * the datastoresyncer can facilitate the management of local CRDs
 */

type OnClusterChange func(cluster *types.SubmarinerCluster, deleted bool) error
type OnEndpointChange func(endpoint *types.SubmarinerEndpoint, deleted bool) error

type Datastore interface {
	// This gets all endpoints for the given cluster ID
	GetEndpoints(clusterID string) ([]types.SubmarinerEndpoint, error)

	// Watches all clusters and calls the passed in function on cluster change
	WatchClusters(ctx context.Context, selfClusterID string, colorCodes []string, onClusterChange OnClusterChange) error

	// Performs a watch of all endpoints and calls the passed in function based on information
	WatchEndpoints(ctx context.Context, selfClusterID string, colorCodes []string, onEndpointChange OnEndpointChange) error

	// This should be called to set the local cluster information.
	SetCluster(cluster *types.SubmarinerCluster) error

	// This should only ever be called to set the endpoint of the local node.
	SetEndpoint(endpoint *types.SubmarinerEndpoint) error

	// This should be called to remove an endpoint from use
	RemoveEndpoint(clusterID, cableName string) error
}
