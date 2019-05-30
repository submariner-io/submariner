package util_test

import (
	"testing"

	subv1 "github.com/rancher/submariner/pkg/apis/submariner.io/v1"
	"github.com/rancher/submariner/pkg/types"
	"github.com/rancher/submariner/pkg/util"
	"github.com/stretchr/testify/assert"
)

func TestParseSecure(t *testing.T) {
	apiKey := "AValidAPIKeyWithLengthThirtyTwo!"
	secretKey := "AValidSecretKeyWithLength32Chars"
	secure, err := util.ParseSecure(apiKey + secretKey)

	assert := assert.New(t)
	if !assert.NoError(err) {
		return
	}
		
	assert.Equal(apiKey, secure.ApiKey, "ApiKey")
	assert.Equal(secretKey, secure.SecretKey, "SecretKey")
}

func TestFlattenColorsWithSingleElement(t *testing.T) {
	assert.Equal(t, "blue", util.FlattenColors([]string{"blue"}))
}

func TestFlattenColorsWithMultipleElements(t *testing.T) {
	assert.Equal(t, "red,white,blue", util.FlattenColors([]string{"red", "white", "blue"}))
}

func TestGetLocalCluster(t *testing.T) {
	clusterId := "east"
	clusterCidr := []string{"1.2.3.4/16"}
	serviceCidr := []string{"5.6.7.8/16"}
	colorCodes := []string{"red"}
	cluster, err := util.GetLocalCluster(types.SubmarinerSpecification {
		ClusterId: clusterId,
		ClusterCidr: clusterCidr,
		ServiceCidr: serviceCidr,
		ColorCodes: colorCodes,
	})

	assert := assert.New(t)
	if !assert.NoError(err) {
		return
	}

	assert.Equal(clusterId, cluster.ID, "ID")
	assert.Equal(clusterId, cluster.Spec.ClusterID, "ClusterID")
	assert.Equal(serviceCidr, cluster.Spec.ServiceCIDR, "ServiceCIDR")
	assert.Equal(clusterCidr, cluster.Spec.ClusterCIDR, "ClusterCIDR")
	assert.Equal(colorCodes, cluster.Spec.ColorCodes, "ColorCodes")
}

func TestGetClusterIdFromCableNameWithSimpleClusterID(t *testing.T) {
	assert.Equal(t, "east", util.GetClusterIdFromCableName("submariner-cable-east-172-16-32-5"))
}

func TestGetClusterIdFromCableNameWithClusterIDContainingDashes(t *testing.T) {
	assert.Equal(t, "my-super-long_cluster-id",
		util.GetClusterIdFromCableName("submariner-cable-my-super-long_cluster-id-172-16-32-5"))
}

func TestGetEndpointCRDNameWithValidInput(t *testing.T) {
	name, err := util.GetEndpointCRDName(types.SubmarinerEndpoint {
		Spec: subv1.EndpointSpec{
			ClusterID: "ClusterID",
			CableName: "CableName",
		},
	})

	if !assert.NoError(t, err) {
		return
	}

	assert.Equal(t, "ClusterID-CableName", name)
}

func TestGetEndpointCRDNameWithNilClusterID(t *testing.T) {
	_, err := util.GetEndpointCRDName(types.SubmarinerEndpoint {
		Spec: subv1.EndpointSpec{
			CableName: "CableName",
		},
	})

	assert.Error(t, err)
}

func TestGetEndpointCRDNameWithNilClusterName(t *testing.T) {
	_, err := util.GetEndpointCRDName(types.SubmarinerEndpoint {
		Spec: subv1.EndpointSpec{
			ClusterID: "ClusterID",
		},
	})

	assert.Error(t, err)
}

func TestGetClusterCRDNameWithValidInput(t *testing.T) {
	name, err := util.GetClusterCRDName(types.SubmarinerCluster {
		Spec: subv1.ClusterSpec{
			ClusterID: "ClusterID",
		},
	})

	if !assert.NoError(t, err) {
		return
	}

	assert.Equal(t, "ClusterID", name)
}

func TestCompareEndpointSpecWithEqualInput(t *testing.T) {
	assert.True(t, util.CompareEndpointSpec(
		subv1.EndpointSpec {
			ClusterID: "east",
			CableName: "submariner-cable-east-172-16-32-5",
			Hostname: "my-host",
		},
		subv1.EndpointSpec {
			ClusterID: "east",
			CableName: "submariner-cable-east-172-16-32-5",
			Hostname: "my-host",
		}))
}

func TestCompareEndpointSpecWithDifferingClusterIDs(t *testing.T) {
	assert.False(t, util.CompareEndpointSpec(
		subv1.EndpointSpec {
			ClusterID: "east",
			CableName: "submariner-cable-east-172-16-32-5",
			Hostname: "my-host",
		},
		subv1.EndpointSpec {
			ClusterID: "west",
			CableName: "submariner-cable-east-172-16-32-5",
			Hostname: "my-host",
		}))
}

func TestCompareEndpointSpecWithDifferingCableNames(t *testing.T) {
	assert.False(t, util.CompareEndpointSpec(
		subv1.EndpointSpec {
			ClusterID: "east",
			CableName: "submariner-cable-east-1-2-3-4",
			Hostname: "my-host",
		},
		subv1.EndpointSpec {
			ClusterID: "east",
			CableName: "submariner-cable-east-5-6-7-8",
			Hostname: "my-host",
		}))
}

func TestCompareEndpointSpecWithDifferingHostNames(t *testing.T) {
	assert.False(t, util.CompareEndpointSpec(
		subv1.EndpointSpec {
			ClusterID: "east",
			CableName: "submariner-cable-east-172-16-32-5",
			Hostname: "host1",
		},
		subv1.EndpointSpec {
			ClusterID: "east",
			CableName: "submariner-cable-east-172-16-32-5",
			Hostname: "host2",
		}))
}