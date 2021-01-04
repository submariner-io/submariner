/*
© 2021 Red Hat, Inc. and others

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
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

	RouteAgentInterClusterNetworkTableID = 149

	// To support connectivity for Pods with HostNetworking on the GatewayNode, we program
	// certain routing rules in table 150. As part of these routes, we set the source-ip of
	// the egress traffic to the corresponding CNIInterfaceIP on that host.
	RouteAgentHostNetworkTableID = 150
)
