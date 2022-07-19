/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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

package ovn

import "time"

const (
	submarinerLogicalRouter = "submariner_router"
	ovnClusterRouter        = "ovn_cluster_router"
	ovnLBGroup              = "clusterLBGroup"
	// The downstream port connects to ovn_cluster_router, allowing connectivity to pods and services.
	submarinerDownstreamSwitch = "submariner_join"
	submarinerDownstreamRPort  = "submariner_j_lrp"
	submarinerDownstreamSwPort = "submariner_j_lsp"
	submarinerDownstreamMAC    = "00:60:2f:10:01:03"
	submarinerDownstreamNET    = submarinerDownstreamIP + "/29"
	submarinerDownstreamIP     = "169.254.254.1"
	// The upstream port connects to the host on the GW, right before/after the encryption cable driver.
	submarinerUpstreamSwPort       = "submariner_up_lsp"
	submarinerUpstreamRPort        = "submariner_up_lrp"
	submarinerUpstreamMAC          = "00:60:2f:10:01:01"
	submarinerUpstreamNET          = SubmarinerUpstreamIP + "/29"
	SubmarinerUpstreamIP           = "169.254.254.9"  // public constant, used in the route-agent handler
	HostUpstreamIP                 = "169.254.254.10" // we configure this one as ovn-k8s-sub0 interface on each worker node
	HostUpstreamNET                = HostUpstreamIP + "/29"
	SubmarinerUpstreamLocalnet     = "submariner_gateway"
	submarinerUpstreamSwitch       = "submariner_gateway" // this switch is mapped as br-submariner on each worker node
	submarinerUpstreamLocalnetPort = "submariner_localnet"
	// The ovn_cluster_router submariner port connects to the submariner router.
	ovnClusterSubmarinerRPort  = "ovn_cluster_subm_lrp"
	ovnClusterSubmarinerSwPort = "ovn_cluster_subm_lsp"
	ovnClusterSubmarinerMAC    = "00:60:2f:10:01:02"
	ovnClusterSubmarinerNET    = ovnClusterSubmarinerIP + "/29"
	ovnClusterSubmarinerIP     = "169.254.254.2"
	ovnRoutePoliciesPrio       = 20000

	// default ovsdb timout used by ovn-k.
	OVSDBTimeout   = 10 * time.Second
	ovnCert        = "secret://openshift-ovn-kubernetes/ovn-cert/tls.crt"
	ovnPrivKey     = "secret://openshift-ovn-kubernetes/ovn-cert/tls.key"
	ovnCABundle    = "configmap://openshift-ovn-kubernetes/ovn-ca/ca-bundle.crt"
	defaultOVNNBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9641"
	defaultOVNSBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9642"
)
