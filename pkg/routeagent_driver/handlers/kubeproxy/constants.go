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

package kubeproxy

const (
	VxLANIface         = "vx-submariner"
	VxInterfaceWorker  = 0
	VxInterfaceGateway = 1
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
)

type Operation int

const (
	Add Operation = iota
	Delete
	Flush
)
