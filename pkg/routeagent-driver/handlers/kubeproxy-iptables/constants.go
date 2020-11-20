package kp_iptables

const (
	VxLANIface         = "vx-submariner"
	VxInterfaceWorker  = 0
	VxInterfaceGateway = 1
	VxLANPort          = 4800
	VxLANOverhead      = 50

	// Why VxLANVTepNetworkPrefix is 240?
	// On VxLAN interfaces we need a unique IPAddress which does not collide with the
	// host ip-address. This is going to be tricky as currently there is no specific
	// CIDR in K8s that can be used for this purpose. One option is to take this as an
	// input from the user (i.e., as a configuration parameter), but we want to avoid
	// any additional inputs particularly if there is a way to automate it.

	// So, the approach we are taking is to derive the VxLAN ip from the hostIPAddress
	// as shown below.
	// For example: Say, the host ipaddress is "192.168.1.100/16", we prepend 240 to the
	// host-ip address, derive the vxlan vtepIP (i.e., 240.168.1.100/8) and configure it
	// on the VxLAN interface.

	// The reason behind choosing 240 is that "240.0.0.0/4" is a Reserved IPAddress [*]
	// which normally will not be assigned on any of the hosts. Also, note that the VxLAN
	// IPs are only used within the local cluster and traffic will not leave the cluster
	// with the VxLAN ipaddress.
	// [*] https://en.wikipedia.org/wiki/Reserved_IP_addresses

	VxLANVTepNetworkPrefix = 240
	SmRouteAgentFilter     = "app=submariner-routeagent"

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
	CniInterfaceIp = "submariner.io/cniIfaceIp"

	// To support connectivity for Pods with HostNetworking on the GatewayNode, we program
	// certain routing rules in table 150. As part of these routes, we set the source-ip of
	// the egress traffic to the corresponding CNIInterfaceIP on that host.
	RouteAgentHostNetworkTableID = 150
)

type Operation int

const (
	Add Operation = iota
	Delete
	Flush
)
