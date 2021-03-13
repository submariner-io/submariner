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
package ipam

type Operation string

const (
	SubmarinerIPAMGlobalIP = "submariner.io/globalIp"

	// Globalnet uses MARK target to mark traffic destined to remote clusters.
	// Some of the CNIs also use iptable MARK targets in the pipeline. This should not
	// be a problem because Globalnet is only marking traffic destined to Submariner
	// connected clusters where Submariner takes full control on how the traffic is
	// steered in the pipeline. Normal traffic should not be affected because of this.
	globalNetIPTableMark = "0xC0000/0xC0000"

	// Currently Submariner Globalnet implementation (for services) works with kube-proxy
	// and uses iptable chain-names programmed by kube-proxy. If the internal implementation
	// of kube-proxy changes, globalnet needs to be modified accordingly.
	// Reference: https://bit.ly/2OPhlwk
	kubeProxyServiceChainPrefix = "KUBE-SVC-"
	kubeProxyServiceChainName   = "KUBE-SERVICES"

	maxServiceRequeues = 20

	AddRules    = true
	DeleteRules = false
)
