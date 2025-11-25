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

package configure

import (
	"github.com/submariner-io/admiral/pkg/global"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/packetfilter/iptables"
	"github.com/submariner-io/submariner/pkg/packetfilter/nftables"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type DriverType int

const (
	IPTables DriverType = iota
	NfTables
)

const UseNftablesKey = "use-nftables"

var logger = log.Logger{Logger: logf.Log.WithName("Packetfilter")}

func DriverFromGlobalConfig() {
	useNftables := global.Get(UseNftablesKey, true)

	if useNftables {
		logger.Info("Using nftables packet filter driver")
		packetfilter.SetNewDriverFn(nftables.New)
	} else {
		logger.Info("Using iptables packet filter driver")
		packetfilter.SetNewDriverFn(iptables.New)
	}
}

func GetDriverType() DriverType {
	if global.Get(UseNftablesKey, true) {
		return NfTables
	}

	return IPTables
}
