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

package netlink

import "net"

type NetworkInterface interface {
	Index() int
	MTU() int
	Name() string
	HardwareAddr() net.HardwareAddr
	Flags() net.Flags
	Addrs() ([]net.Addr, error)
}

type DefaultNetworkInterface struct {
	net.Interface
}

func (i *DefaultNetworkInterface) Index() int {
	return i.Interface.Index
}

func (i *DefaultNetworkInterface) MTU() int {
	return i.Interface.MTU
}

func (i *DefaultNetworkInterface) Name() string {
	return i.Interface.Name
}

func (i *DefaultNetworkInterface) HardwareAddr() net.HardwareAddr {
	return i.Interface.HardwareAddr
}

func (i *DefaultNetworkInterface) Flags() net.Flags {
	return i.Interface.Flags
}
