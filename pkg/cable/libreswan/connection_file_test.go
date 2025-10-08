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

package libreswan_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/cable/libreswan"
)

var (
	connStanzaTemplate = `conn %s
left=%s
right=%s
type=tunnel
`

	connName1   = "cable-ipv4-0-0"
	connStanza1 = fmt.Sprintf(connStanzaTemplate, connName1, "0", "0")

	connName2   = "cable-ipv4-1-1"
	connStanza2 = fmt.Sprintf(connStanzaTemplate, connName2, "1", "1")

	connName3   = "cable-ipv4-2-2"
	connStanza3 = fmt.Sprintf(connStanzaTemplate, connName3, "2", "2")
)

var _ = Describe("ConnectionFile", func() {
	var connFile *libreswan.ConnectionFile

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "libreswan_test")
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			Expect(os.RemoveAll(dir)).To(Succeed())
		})

		connFile = &libreswan.ConnectionFile{Path: filepath.Join(dir, "submariner.conf")}
	})

	Specify("should correctly append and remove stanzas from the file", func() {
		Expect(connFile.AppendConnectionStanza(connStanza1, connName1)).To(Succeed())
		verifyConnectionFile(connFile.Path, connStanza1)

		Expect(connFile.AppendConnectionStanza(connStanza2, connName2)).To(Succeed())
		verifyConnectionFile(connFile.Path, connStanza1+connStanza2)

		Expect(connFile.AppendConnectionStanza(connStanza3, connName3)).To(Succeed())
		verifyConnectionFile(connFile.Path, connStanza1+connStanza2+connStanza3)

		Expect(connFile.RemoveConnectionStanza(connName2)).To(Succeed())
		verifyConnectionFile(connFile.Path, connStanza1+connStanza3)

		Expect(connFile.RemoveConnectionStanza(connName3)).To(Succeed())
		verifyConnectionFile(connFile.Path, connStanza1)

		Expect(connFile.RemoveConnectionStanza(connName1)).To(Succeed())
		_, err := os.Stat(connFile.Path)
		Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
	})

	When("a stanza for a connection already exists", func() {
		Specify("AppendConnectionStanza should replace it", func() {
			existing := fmt.Sprintf(connStanzaTemplate, connName1, "9", "9")
			Expect(connFile.AppendConnectionStanza(existing, connName1)).To(Succeed())
			verifyConnectionFile(connFile.Path, existing)

			Expect(connFile.AppendConnectionStanza(connStanza1, connName1)).To(Succeed())
			verifyConnectionFile(connFile.Path, connStanza1)
		})
	})
})

func verifyConnectionFile(path, contents string) {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	Expect(string(data)).To(Equal(contents), "file data: %s", string(data))
}
