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

package amneziawg

import (
	"fmt"
	"strings"

	"github.com/advanced-wg/awgctrl-go/wgtypes"
	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun/tuntest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newRealAmneziaDevice starts the actual amnezia-vpn/amneziawg-go device (the same package
// StartUserspaceDevice embeds in daemon.go) over an in-memory TUN. It exercises the real UAPI
// protocol parser, unlike the fakeClient used elsewhere in this package's tests, which accepts
// any string and so cannot catch a driver default that the real device rejects.
func newRealAmneziaDevice() *device.Device {
	return device.NewDevice(tuntest.NewChannelTUN().TUN(), conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
}

// uapiSetConfig renders the obfuscation fields applyCableDriverOptionsMap populates in the same
// wire format awgctrl-go's internal/wguser.writeConfig sends over the UAPI "set" socket.
func uapiSetConfig(cfg *wgtypes.Config) string {
	var b strings.Builder

	fmt.Fprintf(&b, "jc=%d\njmin=%d\njmax=%d\n", *cfg.Jc, *cfg.Jmin, *cfg.Jmax)
	fmt.Fprintf(&b, "s1=%d\ns2=%d\n", *cfg.S1, *cfg.S2)
	fmt.Fprintf(&b, "h1=%s\nh2=%s\nh3=%s\nh4=%s\n", *cfg.H1, *cfg.H2, *cfg.H3, *cfg.H4)
	fmt.Fprintf(&b, "i1=%s\ni2=%s\ni3=%s\ni4=%s\ni5=%s\n", *cfg.I1, *cfg.I2, *cfg.I3, *cfg.I4, *cfg.I5)
	b.WriteString("\n")

	return b.String()
}

var _ = Describe("default obfuscation config against the real AmneziaWG device", func() {
	It("should be accepted by the vendored amneziawg-go UAPI parser", func() {
		var cfg wgtypes.Config
		Expect(applyCableDriverOptionsMap(newOptionsDriver(), &cfg, nil)).To(Succeed())

		dev := newRealAmneziaDevice()
		defer dev.Close()

		Expect(dev.IpcSet(uapiSetConfig(&cfg))).To(Succeed())
	})
})
