package v1

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const expectedString = `{"metadata":{"creationTimestamp":null},"spec":{"cluster_id":"cluster-id","cable_name":` +
	`"cable-1","hostname":"","subnets":["10.0.0.0/24","172.0.0.0/24"],"private_ip":"1.1.1.1",` +
	`"public_ip":"","nat_enabled":false,"backend":""}}`

var _ = Describe("API v1", func() {
	When("Endpoint String representation called", func() {
		It("Should return a human readable string", func() {

			endpoint := Endpoint{
				Spec: EndpointSpec{
					ClusterID: "cluster-id",
					Subnets:   []string{"10.0.0.0/24", "172.0.0.0/24"},
					CableName: "cable-1",
					PublicIP:  "",
					PrivateIP: "1.1.1.1",
				},
			}

			Expect(endpoint.String()).To(Equal(expectedString))
		})
	})
})

func TestApiMethods(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "V1 Api Method suite")
}
