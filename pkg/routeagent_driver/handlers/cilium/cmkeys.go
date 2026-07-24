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

package cilium

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// ClusterMesh etcd key layout mirrors Cilium ClusterMesh (see cilium/pkg/kvstore).
const (
	cmBasePrefix          = "cilium"
	cmHeartbeatKey        = cmBasePrefix + "/.heartbeat"
	cmClusterConfigPrefix = cmBasePrefix + "/cluster-config"
	cmIPStatePrefix       = cmBasePrefix + "/state/ip/v1/default"

	// cmHeartbeatInterval matches cilium/pkg/kvstore.HeartbeatWriteInterval.
	// Cilium agents treat >2× this interval without updates as a dead kvstore.
	cmHeartbeatInterval = time.Minute
)

// defaultRemoteIdentityLocalID is the local identity slice used for remote CIDR entries
// (maxConnectedClusters=255 → clusterID shifted by 16).
const defaultRemoteIdentityLocalID uint32 = 1000

// ciliumClusterConfig mirrors cilium/pkg/clustermesh/types (stable JSON).
type ciliumClusterConfig struct {
	ID           uint32                          `json:"id,omitempty"`
	Capabilities ciliumClusterConfigCapabilities `json:"capabilities,omitzero"`
}

type ciliumClusterConfigCapabilities struct {
	SyncedCanaries       bool   `json:"syncedCanaries,omitempty"`
	Cached               bool   `json:"cached,omitempty"`
	MaxConnectedClusters uint32 `json:"maxConnectedClusters,omitempty"`
}

// ipIdentityPair mirrors cilium/pkg/identity IPIdentityPair (stable JSON).
type ipIdentityPair struct {
	IP     net.IP     `json:"IP"`
	Mask   net.IPMask `json:"Mask"`
	HostIP net.IP     `json:"HostIP"`
	ID     uint32     `json:"ID"`
	Key    uint8      `json:"Key"`
}

func clusterConfigKey(clusterName string) string {
	return cmClusterConfigPrefix + "/" + clusterName
}

func ipIdentityKey(cidrOrIP string) string {
	return cmIPStatePrefix + "/" + cidrOrIP
}

func defaultClusterConfig(id uint32) ciliumClusterConfig {
	return ciliumClusterConfig{
		ID: id,
		Capabilities: ciliumClusterConfigCapabilities{
			MaxConnectedClusters: 255,
		},
	}
}

func identityForCluster(clusterID, localID uint32) uint32 {
	if localID == 0 {
		localID = defaultRemoteIdentityLocalID
	}

	return (clusterID << 16) | (localID & 0xffff)
}

func marshalClusterConfig(cfg ciliumClusterConfig) ([]byte, error) {
	// ciliumClusterConfig only contains JSON-safe types.
	b, err := json.Marshal(cfg) //nolint:errchkjson // safe value types
	if err != nil {
		return nil, errors.Wrap(err, "marshal cluster config")
	}

	return b, nil
}

func marshalIPIdentityPair(pair *ipIdentityPair) ([]byte, error) {
	b, err := json.Marshal(pair)
	if err != nil {
		return nil, errors.Wrap(err, "marshal IP identity pair")
	}

	return b, nil
}

func prefixString(pair *ipIdentityPair) string {
	ipstr := pair.IP.String()
	if pair.Mask == nil {
		return ipstr
	}

	ones, _ := pair.Mask.Size()

	return ipstr + "/" + strconv.Itoa(ones)
}

func parseCIDR(s string) (net.IP, net.IPMask, error) {
	if strings.Contains(s, "/") {
		ip, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, nil, err //nolint:wrapcheck // passthrough
		}

		if v4 := ip.To4(); v4 != nil {
			return v4, ipnet.Mask, nil
		}

		return ip, ipnet.Mask, nil
	}

	ip := net.ParseIP(s)
	if ip == nil {
		return nil, nil, fmt.Errorf("invalid IP or CIDR: %q", s)
	}

	if v4 := ip.To4(); v4 != nil {
		return v4, nil, nil
	}

	return ip, nil, nil
}
