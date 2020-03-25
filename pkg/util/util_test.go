package util_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
)

var _ = Describe("Util", func() {
	Describe("Function ParseSecure", testParseSecure)

	Describe("Function FlattenColors", testFlattenColors)

	Describe("Function GetLocalCluster", testGetLocalCluster)

	Describe("Function GetLocalEndpoint", testGetLocalEndpoint)

	Describe("Function GetClusterIDFromCableName", testGetClusterIDFromCableName)

	Describe("Function GetEndpointCRDName", testGetEndpointCRDName)

	Describe("Function GetClusterCRDName", testGetClusterCRDName)

	Describe("Function CompareEndpointSpec", testCompareEndpointSpec)

})

func testParseSecure() {
	Context("with a valid token input", func() {
		It("should return a valid Secure object", func() {
			apiKey := "AValidAPIKeyWithLengthThirtyTwo!"
			secretKey := "AValidSecretKeyWithLength32Chars"
			secure, err := util.ParseSecure(apiKey + secretKey)
			Expect(err).ToNot(HaveOccurred())
			Expect(secure.APIKey).To(Equal(apiKey))
			Expect(secure.SecretKey).To(Equal(secretKey))
		})
	})

	Context("with an invalid token input", func() {
		It("should return an error", func() {
			_, err := util.ParseSecure("InvalidToken")
			Expect(err).To(HaveOccurred())
		})
	})
}

func testFlattenColors() {
	Context("with a single element", func() {
		It("should return the element", func() {
			Expect(util.FlattenColors([]string{"blue"})).To(Equal("blue"))
		})
	})

	Context("with multiple elements", func() {
		It("should return a comma-separated string containing all the elements", func() {
			Expect(util.FlattenColors([]string{"red", "white", "blue"})).To(Equal("red,white,blue"))
		})
	})

	Context("with no elements", func() {
		It("should return an empty string", func() {
			Expect(util.FlattenColors([]string{})).To(Equal(""))
		})
	})
}

func testGetLocalCluster() {
	It("should return a valid SubmarinerCluster object", func() {
		clusterId := "east"
		clusterCidr := []string{"1.2.3.4/16"}
		serviceCidr := []string{"5.6.7.8/16"}
		colorCodes := []string{"red"}
		cluster, err := util.GetLocalCluster(types.SubmarinerSpecification{
			ClusterID:   clusterId,
			ClusterCidr: clusterCidr,
			ServiceCidr: serviceCidr,
			ColorCodes:  colorCodes,
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.ID).To(Equal(clusterId))
		Expect(cluster.Spec.ClusterID).To(Equal(clusterId))
		Expect(cluster.Spec.ServiceCIDR).To(Equal(serviceCidr))
		Expect(cluster.Spec.ClusterCIDR).To(Equal(clusterCidr))
		Expect(cluster.Spec.ColorCodes).To(Equal(colorCodes))
	})
}

func testGetLocalEndpoint() {
	It("should return a valid SubmarinerEndpoint object", func() {
		subnets := []string{"1.2.3.4/16"}
		privateIP := "1.2.3.4"
		endpoint, err := util.GetLocalEndpoint("east", "backend", map[string]string{}, false, subnets, privateIP)

		Expect(err).ToNot(HaveOccurred())
		Expect(endpoint.Spec.ClusterID).To(Equal("east"))
		Expect(endpoint.Spec.CableName).To(HavePrefix("submariner-cable-east-"))
		Expect(endpoint.Spec.Hostname).NotTo(Equal(""))
		Expect(endpoint.Spec.PrivateIP).To(Equal(privateIP))
		Expect(endpoint.Spec.Backend).To(Equal("backend"))
		Expect(endpoint.Spec.Subnets).To(Equal(subnets))
		Expect(endpoint.Spec.NATEnabled).To(Equal(false))
	})
}

func testGetClusterIDFromCableName() {
	Context("with a simple embedded cluster ID", func() {
		It("should extract and return the cluster ID", func() {
			Expect(util.GetClusterIDFromCableName("submariner-cable-east-172-16-32-5")).To(Equal("east"))
		})
	})

	Context("with an embedded cluster ID containing dashes", func() {
		It("should extract and return the cluster ID", func() {
			Expect(util.GetClusterIDFromCableName("submariner-cable-my-super-long_cluster-id-172-16-32-5")).To(
				Equal("my-super-long_cluster-id"))
		})
	})
}

func testGetEndpointCRDName() {
	Context("with valid SubmarinerEndpoint input", func() {
		It("should return <cluster ID>-<cable name>", func() {
			name, err := util.GetEndpointCRDName(&types.SubmarinerEndpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "ClusterID",
					CableName: "CableName",
				},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(name).To(Equal("ClusterID-CableName"))
		})
	})

	Context("with a nil cluster ID", func() {
		It("should return an error", func() {
			_, err := util.GetEndpointCRDName(&types.SubmarinerEndpoint{
				Spec: subv1.EndpointSpec{
					CableName: "CableName",
				},
			})

			Expect(err).To(HaveOccurred())
		})
	})

	Context("with a nil cable name", func() {
		It("should return an error", func() {
			_, err := util.GetEndpointCRDName(&types.SubmarinerEndpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "ClusterID",
				},
			})

			Expect(err).To(HaveOccurred())
		})
	})
}

func testGetClusterCRDName() {
	Context("with valid input", func() {
		It("should return the cluster ID", func() {
			Expect(util.GetClusterCRDName(&types.SubmarinerCluster{
				Spec: subv1.ClusterSpec{
					ClusterID: "ClusterID",
				},
			})).To(Equal("ClusterID"))
		})
	})

	Context("with a nil cluster ID", func() {
		It("should return an error", func() {
			_, err := util.GetClusterCRDName(&types.SubmarinerCluster{
				Spec: subv1.ClusterSpec{},
			})

			Expect(err).To(HaveOccurred())
		})
	})
}

func testCompareEndpointSpec() {
	Context("with equal input", func() {
		It("should return true", func() {
			Expect(util.CompareEndpointSpec(
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "strongswan",
				},
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "strongswan",
				})).To(BeTrue())
		})
	})

	Context("with equal input (include backend map)", func() {
		It("should return true", func() {
			Expect(util.CompareEndpointSpec(
				subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "my-host",
					Backend:       "strongswan",
					BackendConfig: map[string]string{"key": "aaa"},
				},
				subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "my-host",
					Backend:       "strongswan",
					BackendConfig: map[string]string{"key": "aaa"},
				})).To(BeTrue())
		})
	})

	Context("with different cluster IDs", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "strongswan",
				},
				subv1.EndpointSpec{
					ClusterID: "west",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "strongswan",
				})).To(BeFalse())
		})
	})

	Context("with different cable names", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-1-2-3-4",
					Hostname:  "my-host",
					Backend:   "strongswan",
				},
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-5-6-7-8",
					Hostname:  "my-host",
					Backend:   "strongswan",
				})).To(BeFalse())
		})
	})

	Context("with different host names", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "host1",
					Backend:   "strongswan",
				},
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "host2",
					Backend:   "strongswan",
				})).To(BeFalse())
		})
	})

	Context("with different backend names", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "host1",
					Backend:   "strongswan",
				},
				subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "host1",
					Backend:   "libreswan",
				})).To(BeFalse())
		})
	})

	Context("with different backend parameters", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "host1",
					Backend:       "strongswan",
					BackendConfig: map[string]string{"key": "aaa"},
				},
				subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "host1",
					Backend:       "strongswan",
					BackendConfig: map[string]string{"key": "bbb"},
				})).To(BeFalse())
		})
	})
}
