package ipsec

import (
	"github.com/bronze1man/goStrongswanVici"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

var _ = Describe("Strongswan Connection status", func() {
	const connectingState = "CONNECTING"
	const establishedState = "ESTABLISHED"
	const testCable1 = "cable-1"
	const testCable2 = "cable-2"

	DescribeTable("updateConnectionState",
		func(saState string, expectedState v1.ConnectionStatus) {
			sa := goStrongswanVici.IkeSa{State: saState}
			connection := v1.NewConnection(v1.EndpointSpec{})
			updateConnectionState(&sa, connection)
			Expect(connection.Status).To(Equal(expectedState))
		},
		Entry("created state", "CREATED", v1.ConnectionError),
		Entry("connecting state", connectingState, v1.Connecting),
		Entry("established state", establishedState, v1.Connected),
		Entry("passive state", "PASSIVE", v1.ConnectionError),
		Entry("rekeying state", "REKEYING", v1.Connected),
		Entry("rekeyed state", "REKEYED", v1.Connected),
		Entry("deleting state", "DELETING", v1.ConnectionError),
		Entry("destroying state", "DESTROYING", v1.ConnectionError),
		Entry("unknown state", "NOTKNOWNSTATE?", v1.ConnectionError),
	)

	Describe("getSAListConnections", func() {

		var f strongswanConnectionsTest
		BeforeEach(func() {
			f = newStrongswanConnectionsTest()
		})

		When("provided an empty list of remoteEndpoints", func() {
			It("should return empty list of connections", func() {
				f.getConnections()
			})
		})

		When("provided a remoteEndpoint, but empty list of SAs", func() {
			It("should return the remoteEndpoint as failed", func() {
				f.addRemoteEndpoint(testCable1, v1.EndpointSpec{CableName: "not-found-cable"})

				connections := f.getConnections()
				Expect(*connections).To(HaveLen(1))
				Expect((*connections)[0].Status).To(Equal(v1.ConnectionError))
			})
		})

		When("provided a remoteEndpoint, and contained in list of SAs as ESTABLISHED", func() {
			It("should return the remoteEndpoint as connected", func() {
				f.addRemoteEndpoint(testCable1, v1.EndpointSpec{CableName: testCable1})
				f.addSA(testCable1, goStrongswanVici.IkeSa{State: establishedState})

				connections := f.getConnections()
				Expect(*connections).To(HaveLen(1))
				Expect((*connections)[0].Endpoint.CableName).To(Equal(testCable1))
				Expect((*connections)[0].Status).To(Equal(v1.Connected))
			})
		})

		When("provided multiple remoteEndpoint, with SAs out of order", func() {
			It("should return each connection properly", func() {

				f.addRemoteEndpoint(testCable1, v1.EndpointSpec{CableName: testCable1})
				f.addRemoteEndpoint(testCable2, v1.EndpointSpec{CableName: testCable2})

				f.addSA(testCable2, goStrongswanVici.IkeSa{State: connectingState})
				f.addSA(testCable1, goStrongswanVici.IkeSa{State: establishedState})

				connections := f.getConnections()
				Expect(*connections).To(HaveLen(2))

				Expect((*connections)[0].Endpoint.CableName).To(Equal(testCable1))
				Expect((*connections)[0].Status).To(Equal(v1.Connected))

				Expect((*connections)[1].Endpoint.CableName).To(Equal(testCable2))
				Expect((*connections)[1].Status).To(Equal(v1.Connecting))
			})
		})

		When("provided multiple endpoints, one not contained in list of SAs", func() {
			It("should return the remoteEndpoint as error for the non-contained", func() {

				f.addRemoteEndpoint(testCable1, v1.EndpointSpec{CableName: testCable1})
				f.addRemoteEndpoint(testCable2, v1.EndpointSpec{CableName: testCable2})

				f.addSA(testCable1, goStrongswanVici.IkeSa{State: establishedState})

				connections := f.getConnections()
				Expect(*connections).To(HaveLen(2))

				if (*connections)[0].Endpoint.CableName == testCable1 {
					Expect((*connections)[0].Status).To(Equal(v1.Connected))
					Expect((*connections)[1].Endpoint.CableName).To(Equal(testCable2))
					Expect((*connections)[1].Status).To(Equal(v1.ConnectionError))
				} else {
					Expect((*connections)[0].Status).To(Equal(v1.ConnectionError))
					Expect((*connections)[1].Endpoint.CableName).To(Equal(testCable1))
					Expect((*connections)[1].Status).To(Equal(v1.Connected))
				}
			})
		})
	})
})

type strongswanConnectionsTest struct {
	saList []map[string]goStrongswanVici.IkeSa
	ss     strongSwan
}

func newStrongswanConnectionsTest() strongswanConnectionsTest {
	return strongswanConnectionsTest{
		ss:     strongSwan{remoteEndpoints: map[string]v1.EndpointSpec{}},
		saList: []map[string]goStrongswanVici.IkeSa{},
	}
}

func (st *strongswanConnectionsTest) addRemoteEndpoint(cableID string, endpoint v1.EndpointSpec) {
	st.ss.remoteEndpoints[cableID] = endpoint
}

func (st *strongswanConnectionsTest) addSA(cableID string, ikeSA goStrongswanVici.IkeSa) {
	entry := map[string]goStrongswanVici.IkeSa{cableID: ikeSA}
	st.saList = append(st.saList, entry)
}

func (st *strongswanConnectionsTest) getConnections() *[]v1.Connection {
	connections, err := st.ss.getSAListConnections(st.saList)
	Expect(err).ShouldNot(HaveOccurred())
	Expect(connections).ToNot(BeNil())
	return connections
}
