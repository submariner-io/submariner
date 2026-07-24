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

package cilium //nolint:testpackage // Tests exercise unexported ClusterMesh key helpers.

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ClusterMesh keys", func() {
	It("should build cluster-config and IPIdentityPair keys", func() {
		Expect(cmHeartbeatKey).To(Equal("cilium/.heartbeat"))
		Expect(clusterConfigKey("submariner")).To(Equal("cilium/cluster-config/submariner"))
		Expect(ipIdentityKey("10.151.0.0/16")).To(Equal("cilium/state/ip/v1/default/10.151.0.0/16"))
	})

	It("should encode IPIdentityPair JSON with HostIP and identity", func() {
		pair, key, err := buildIPIdentityPair("10.151.0.0/16", "46.224.67.227", 99)
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal("cilium/state/ip/v1/default/10.151.0.0/16"))
		Expect(pair.ID).To(Equal(identityForCluster(99, 1000)))
		Expect(pair.ID).To(Equal(uint32(6489064)))

		b, err := marshalIPIdentityPair(pair)
		Expect(err).NotTo(HaveOccurred())

		var decoded map[string]any
		Expect(json.Unmarshal(b, &decoded)).To(Succeed())
		Expect(decoded["HostIP"]).To(Equal("46.224.67.227"))
		Expect(decoded["IP"]).To(Equal("10.151.0.0"))
		Expect(decoded["Key"]).To(BeNumerically("==", 0))
	})

	It("should marshal default cluster-config", func() {
		b, err := marshalClusterConfig(defaultClusterConfig(99))
		Expect(err).NotTo(HaveOccurred())

		var cfg ciliumClusterConfig
		Expect(json.Unmarshal(b, &cfg)).To(Succeed())
		Expect(cfg.ID).To(Equal(uint32(99)))
		Expect(cfg.Capabilities.MaxConnectedClusters).To(Equal(uint32(255)))
	})
})
