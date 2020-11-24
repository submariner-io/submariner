package constants

const (
	SmGlobalnetIngressChain = "SUBMARINER-GN-INGRESS"
	SmGlobalnetEgressChain  = "SUBMARINER-GN-EGRESS"
	SmGlobalnetMarkChain    = "SUBMARINER-GN-MARK"

	// IPTable chains used by RouteAgent
	SmPostRoutingChain = "SUBMARINER-POSTROUTING"

	// In order to support connectivity from HostNetwork to remoteCluster, route-agent tries
	// to discover the CNIInterface[#] on the respective node and does SNAT of outgoing
	// traffic from that node to the corresponding CNIInterfaceIP. It is to be noted that
	// only traffic destined to the remoteClusters connected via Submariner is SNAT'ed and not
	// any other traffic.
	// At the same time, when Globalnet controller is deployed (i.e., clusters with overlapping
	// Service/Cluster CIDRs) it needs this information so that it can map the CNIInterfaceIP
	// with the corresponding globalIP assigned to the node. Since globalnet controller does
	// not run on all the worker-nodes and there is no well defined mechanism to get the
	// CNIInterfaceIP for each of the nodes, we annotate the node with CNIInterfaceIPInfo as
	// part of route-agent and this will subsequently be used in globalnet controller for
	// supporting connectivity from HostNetwork to remoteClusters.
	// [#] interface on the node that has an IPAddress from the clusterCIDR
	CniInterfaceIP = "submariner.io/cniIfaceIp"
)

type Specification struct {
	ClusterID   string
	Namespace   string
	ClusterCidr []string
	ServiceCidr []string
}
