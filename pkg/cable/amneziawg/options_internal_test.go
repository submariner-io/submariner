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
	"time"

	"github.com/advanced-wg/awgctrl-go/wgtypes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newOptionsDriver() *amneziawgDriver {
	return &amneziawgDriver{keepAlive: 10 * time.Second}
}

var _ = Describe("parseHeaderRange", func() {
	DescribeTable("validation",
		func(value string, wantErr bool) {
			_, err := parseHeaderRange(value)
			if wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("single uint32", "123", false),
		Entry("valid range", "10-20", false),
		Entry("equal bounds", "5-5", false),
		Entry("inverted range", "20-10", true),
		Entry("non-numeric", "foo", true),
		Entry("too many parts", "1-2-3", true),
		Entry("empty", "", true),
		Entry("trailing dash", "10-", true),
		Entry("leading dash", "-10", true),
	)
})

var _ = Describe("validateInitPacketSpec", func() {
	DescribeTable("validation",
		func(value string, wantErr bool) {
			err := validateInitPacketSpec(value)
			if wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("random tag", "<r 40>", false),
		Entry("hex tag", "<b 0xabcd>", false),
		Entry("hex without 0x", "<b abcd>", false),
		Entry("combined tags", "<r 10><b 0xff><t>", false),
		Entry("timestamp tag", "<t>", false),
		Entry("unknown tag", "<lol>", true),
		Entry("plain hex", "abcd", true),
		Entry("empty", "", true),
		Entry("empty tags", "<>", true),
		Entry("unclosed tag", "<r 40", true),
		Entry("negative size", "<r -1>", true),
		Entry("odd hex", "<b abc>", true),
	)
})

var _ = Describe("applyCableDriverOptionsMap", func() {
	It("should apply defaults when options are empty", func() {
		a := newOptionsDriver()
		var cfg wgtypes.Config
		Expect(applyCableDriverOptionsMap(a, &cfg, nil)).To(Succeed())

		Expect(*cfg.Jc).To(Equal(7))
		Expect(*cfg.H1).To(Equal("200000000-280000000"))
		Expect(*cfg.I1).To(Equal("<r 40>"))
		Expect(*cfg.S3).To(Equal(35))
		Expect(a.keepAlive).To(Equal(10 * time.Second))
	})

	It("should apply valid overrides and keep untouched defaults", func() {
		a := newOptionsDriver()
		var cfg wgtypes.Config
		Expect(applyCableDriverOptionsMap(a, &cfg, map[string]string{
			"jc":        "3",
			"h1":        "10-20",
			"i1":        "<b 0x64>",
			"keepalive": "25",
		})).To(Succeed())

		Expect(*cfg.Jc).To(Equal(3))
		Expect(*cfg.H1).To(Equal("10-20"))
		Expect(*cfg.I1).To(Equal("<b 0x64>"))
		Expect(*cfg.S3).To(Equal(35))
		Expect(*cfg.H2).To(Equal("400000000-480000000"))
		Expect(a.keepAlive).To(Equal(25 * time.Second))
	})

	It("should fail on an unknown option key", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{"xyz": "1"})
		Expect(err).To(MatchError(ContainSubstring(`unknown AmneziaWG option "xyz"`)))
	})

	It("should fail on a mistyped option key", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{"Jc": "3"})
		Expect(err).To(MatchError(ContainSubstring(`unknown AmneziaWG option "Jc"`)))
	})

	It("should report multiple option errors together", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{
			"xyz": "1",
			"jc":  "0",
			"h1":  "20-10",
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(And(
			ContainSubstring(`unknown AmneziaWG option "xyz"`),
			ContainSubstring(`invalid AmneziaWG option "jc"="0"`),
			ContainSubstring(`invalid AmneziaWG option "h1"="20-10"`),
		))
	})

	It("should keep defaults for empty option values", func() {
		a := newOptionsDriver()
		var cfg wgtypes.Config
		Expect(applyCableDriverOptionsMap(a, &cfg, map[string]string{
			"jc":        "",
			"h1":        "",
			"i1":        "",
			"keepalive": "",
		})).To(Succeed())

		Expect(*cfg.Jc).To(Equal(7))
		Expect(*cfg.H1).To(Equal("200000000-280000000"))
		Expect(*cfg.I1).To(Equal("<r 40>"))
		Expect(a.keepAlive).To(Equal(10 * time.Second))
	})

	It("should fail on a non-positive junk count", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{"jc": "0"})
		Expect(err).To(MatchError(ContainSubstring(`invalid AmneziaWG option "jc"="0"`)))
	})

	It("should fail on an invalid integer option", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{"jc": "not-a-number"})
		Expect(err).To(MatchError(ContainSubstring(`invalid AmneziaWG option "jc"="not-a-number"`)))
	})

	It("should fail on an invalid header option", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{"h1": "20-10"})
		Expect(err).To(MatchError(ContainSubstring(`invalid AmneziaWG option "h1"="20-10"`)))
	})

	It("should fail when header ranges overlap", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{
			"h1": "10-30",
			"h2": "20-40",
		})
		Expect(err).To(MatchError(ContainSubstring("overlaps")))
	})

	It("should fail on an invalid init packet option", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{"i1": "<lol>"})
		Expect(err).To(MatchError(ContainSubstring(`invalid AmneziaWG option "i1"="<lol>"`)))
	})

	It("should fail on an invalid keepalive option", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{"keepalive": "-1"})
		Expect(err).To(MatchError(ContainSubstring(`invalid AmneziaWG option "keepalive"="-1"`)))
	})

	It("should fail when keepalive exceeds the WireGuard maximum", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{"keepalive": "65536"})
		Expect(err).To(MatchError(ContainSubstring(`invalid AmneziaWG option "keepalive"="65536"`)))
	})

	It("should allow disabling keepalive with 0", func() {
		a := newOptionsDriver()
		Expect(applyCableDriverOptionsMap(a, &wgtypes.Config{}, map[string]string{"keepalive": "0"})).To(Succeed())
		Expect(a.keepAlive).To(Equal(time.Duration(0)))
	})

	It("should fail when jmin is greater than jmax", func() {
		err := applyCableDriverOptionsMap(newOptionsDriver(), &wgtypes.Config{}, map[string]string{
			"jmin": "200",
			"jmax": "80",
		})
		Expect(err).To(MatchError(ContainSubstring("jmin (200) must be <= jmax (80)")))
	})

	It("should not clobber non-obfuscation config fields", func() {
		port := 51820
		cfg := wgtypes.Config{ListenPort: &port}

		Expect(applyCableDriverOptionsMap(newOptionsDriver(), &cfg, map[string]string{"jc": "3"})).To(Succeed())
		Expect(cfg.ListenPort).To(Equal(&port))
		Expect(*cfg.Jc).To(Equal(3))
	})
})
