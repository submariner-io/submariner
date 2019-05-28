package util_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	subv1 "github.com/rancher/submariner/pkg/apis/submariner.io/v1"
	"github.com/rancher/submariner/pkg/types"
	. "github.com/rancher/submariner/pkg/util"
)

var _ = Describe("Util", func() {
	Describe("Function ParseSecure", func() {
		Context("With a valid token input", func() {
			It("should return a valid Secure object", func() {
				apiKey := "AValidAPIKeyWithLengthThirtyTwo!"
				secretKey := "AValidSecretKeyWithLength32Chars"
				secure, err := ParseSecure(apiKey + secretKey)
				Expect(err).ToNot(HaveOccurred())
				Expect(secure.ApiKey).To(Equal(apiKey))
				Expect(secure.SecretKey).To(Equal(secretKey))
			})
		})

		Context("With an invalid token input", func() {
			It("should return an error", func() {
				_, err := ParseSecure("InvalidToken")
				Expect(err).To(HaveOccurred())
			})
		})
	})
	
	Describe("Function GetLocalIP", func() {
		It("should return a non-nil IP", func() {
			Expect(GetLocalIP()).NotTo(BeNil())
		})
	})

	Describe("Function FlattenColors", func() {
		Context("With a single element", func() {
			It("should return the element", func() {
				Expect(FlattenColors([]string{"blue"})).To(Equal("blue"))
			})
		})

		Context("With multiple elements", func() {
			It("should return a comma-separated string containing all the elements", func() {
				Expect(FlattenColors([]string{"red", "white", "blue"})).To(Equal("red,white,blue"))
			})
		})
		
		Context("With no elements", func() {
			It("should return an empty string", func() {
				Expect(FlattenColors([]string{})).To(Equal(""))
			})
		})
	})

	Describe("Function GetLocalCluster", func() {
		It("should return a valid SubmarinerCluster object", func() {
			clusterId := "east"
			clusterCidr := []string{"1.2.3.4/16"}
			serviceCidr := []string{"5.6.7.8/16"}
			colorCodes := []string{"red"}
			cluster, err := GetLocalCluster(types.SubmarinerSpecification {
				ClusterId: clusterId,
				ClusterCidr: clusterCidr,
				ServiceCidr: serviceCidr,
				ColorCodes: colorCodes,
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.ID).To(Equal(clusterId))
			Expect(cluster.Spec.ClusterID).To(Equal(clusterId))
			Expect(cluster.Spec.ServiceCIDR).To(Equal(serviceCidr))
			Expect(cluster.Spec.ClusterCIDR).To(Equal(clusterCidr))
			Expect(cluster.Spec.ColorCodes).To(Equal(colorCodes))
		})
	})

	Describe("Function GetLocalEndpoint", func() {
		It("should return a valid SubmarinerEndpoint object", func() {
			subnets := []string{"1.2.3.4/16"}
			endpoint, err := GetLocalEndpoint("east", "backend", map[string]string{}, false, subnets)

			Expect(err).ToNot(HaveOccurred())
			Expect(endpoint.Spec.ClusterID).To(Equal("east"))
			Expect(endpoint.Spec.CableName).To(HavePrefix("submariner-cable-east-"))
			Expect(endpoint.Spec.Hostname).NotTo(Equal(""))
			Expect(endpoint.Spec.PrivateIP).NotTo(BeNil())
			Expect(endpoint.Spec.Backend).To(Equal("backend"))
			Expect(endpoint.Spec.Subnets).To(Equal(subnets))
			Expect(endpoint.Spec.NATEnabled).To(Equal(false))
		})
	})

	Describe("Function GetClusterIdFromCableName", func() {
		Context("With a simple embedded cluster ID", func() {
			It("should extract and return the cluster ID", func() {
				Expect(GetClusterIdFromCableName("submariner-cable-east-172-16-32-5")).To(Equal("east"))
			})
		})
	
		Context("With an embedded cluster ID containing dashes", func() {
			It("should extract and return the cluster ID", func() {
				Expect(GetClusterIdFromCableName("submariner-cable-my-super-long_cluster-id-172-16-32-5")).To(
					Equal("my-super-long_cluster-id"))
			})
		})
	})

	Describe("Function GetEndpointCRDName", func() {
		Context("With valid SubmarinerEndpoint input", func() {
			It("should return <cluster ID>-<cable name>", func() {
				name, err := GetEndpointCRDName(types.SubmarinerEndpoint {
					Spec: subv1.EndpointSpec{
						ClusterID: "ClusterID",
						CableName: "CableName",
					},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(name).To(Equal("ClusterID-CableName"))
			})
		})

		Context("With a nil cluster ID", func() {
			It("should return an error", func() {
				_, err := GetEndpointCRDName(types.SubmarinerEndpoint {
					Spec: subv1.EndpointSpec{
						CableName: "CableName",
					},
				})

				Expect(err).To(HaveOccurred())
			})
		})
		
		Context("With a nil cable name", func() {
			It("should return an error", func() {
				_, err := GetEndpointCRDName(types.SubmarinerEndpoint {
					Spec: subv1.EndpointSpec{
						ClusterID: "ClusterID",
					},
				})

				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("Function GetClusterCRDName", func() {
		It("should return the cluster ID", func() {
			Expect(GetClusterCRDName(types.SubmarinerCluster {
				Spec: subv1.ClusterSpec{
					ClusterID: "ClusterID",
				},
			})).To(Equal("ClusterID"))
		})
	})

	Describe("Function CompareEndpointSpec", func() {
		Context("With equal input", func() {
			It("should return true", func() {
				Expect(CompareEndpointSpec(
					subv1.EndpointSpec {
						ClusterID: "east",
						CableName: "submariner-cable-east-172-16-32-5",
						Hostname: "my-host",
					},
					subv1.EndpointSpec {
						ClusterID: "east",
						CableName: "submariner-cable-east-172-16-32-5",
						Hostname: "my-host",
					})).To(BeTrue())
			})
		})

		Context("With different cluster IDs", func() {
			It("should return false", func() {
				Expect(CompareEndpointSpec(
					subv1.EndpointSpec {
						ClusterID: "east",
						CableName: "submariner-cable-east-172-16-32-5",
						Hostname: "my-host",
					},
					subv1.EndpointSpec {
						ClusterID: "west",
						CableName: "submariner-cable-east-172-16-32-5",
						Hostname: "my-host",
					})).To(BeFalse())
			})
		})
		
		Context("With different cable names", func() {
			It("should return false", func() {
				Expect(CompareEndpointSpec(
					subv1.EndpointSpec {
						ClusterID: "east",
						CableName: "submariner-cable-east-1-2-3-4",
						Hostname: "my-host",
					},
					subv1.EndpointSpec {
						ClusterID: "east",
						CableName: "submariner-cable-east-5-6-7-8",
						Hostname: "my-host",
					})).To(BeFalse())
			})
		})

		Context("With different host names", func() {
			It("should return false", func() {
				Expect(CompareEndpointSpec(
					subv1.EndpointSpec {
						ClusterID: "east",
						CableName: "submariner-cable-east-172-16-32-5",
						Hostname: "host1",
					},
					subv1.EndpointSpec {
						ClusterID: "east",
						CableName: "submariner-cable-east-172-16-32-5",
						Hostname: "host2",
					})).To(BeFalse())
			})
		})
	})

	Describe("Function GetDefaultGatewayInterface", func() {
		It("should find and return a non-nil Interface object", func() {
			Expect(GetDefaultGatewayInterface()).NotTo(BeNil())
		})
	})
})
