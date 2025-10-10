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
	"flag"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/submariner/pkg/cable/libreswan"
)

func init() {
	kzerolog.AddFlags(nil)
}

var _ = BeforeSuite(func() {
	flags := flag.NewFlagSet("kzerolog", flag.ExitOnError)
	kzerolog.AddFlags(flags)
	_ = flags.Parse([]string{"-v=4"})

	kzerolog.InitK8sLogging()
})

func TestLibreswan(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Libreswan Suite")
}

func setupTempDir() {
	var err error

	libreswan.RootDir, err = os.MkdirTemp("", "libreswan_test")
	Expect(err).NotTo(HaveOccurred())

	DeferCleanup(func() {
		Expect(os.RemoveAll(libreswan.RootDir)).To(Succeed())
	})
}
