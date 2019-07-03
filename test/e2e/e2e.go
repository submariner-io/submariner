package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	"github.com/onsi/ginkgo/reporters"
	"github.com/onsi/gomega"

	"github.com/submariner-io/submariner/test/e2e/framework"
)

// There are certain operations we only want to run once per overall test invocation
// (such as deleting old namespaces, or verifying that all system pods are running.
// Because of the way Ginkgo runs tests in parallel, we must use SynchronizedBeforeSuite
// to ensure that these operations only run on the first parallel Ginkgo node.
//
// This function takes two parameters: one function which runs on only the first Ginkgo node,
// returning an opaque byte array, and then a second function which runs on all Ginkgo nodes,
// accepting the byte array.
var _ = ginkgo.SynchronizedBeforeSuite(func() []byte {
	// Run only on Ginkgo node 1

	// Wait for readiness of registered clusters to ensure tests
	// run against a healthy federation.
	//framework.WaitForUnmanagedClusterReadiness()

	return nil

}, func(data []byte) {
	// Run on all Ginkgo nodes
})

// Similar to SynchornizedBeforeSuite, we want to run some operations only once (such as collecting cluster logs).
// Here, the order of functions is reversed; first, the function which runs everywhere,
// and then the function that only runs on the first Ginkgo node.

var _ = ginkgo.SynchronizedAfterSuite(func() {
	// Run on all Ginkgo nodes

	//framework.Logf("Running AfterSuite actions on all node")
	framework.RunCleanupActions()
}, func() {
	// Run only Ginkgo on node 1
})

func RunE2ETests(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	// If the ginkgo default for slow test was not modified, bump it to 45 seconds
	if config.DefaultReporterConfig.SlowSpecThreshold == 5.0 {
		config.DefaultReporterConfig.SlowSpecThreshold = 45.0
	}

	// Register the default reporter, and in addition setup the jUnit XML Reporter
	reporterList := []ginkgo.Reporter{}
	reportDir := framework.TestContext.ReportDir
	if reportDir != "" {
		// Create the directory if it doesn't already exists
		if err := os.MkdirAll(reportDir, 0755); err != nil {
			t.Fatalf("Failed creating jUnit report directory: %v", err)
			return
		}
	}
	// Configure a junit reporter to write to the directory
	junitFile := fmt.Sprintf("junit_%s_%02d.xml",
		framework.TestContext.ReportPrefix,
		config.GinkgoConfig.ParallelNode)
	junitPath := filepath.Join(reportDir, junitFile)
	reporterList = append(reporterList, reporters.NewJUnitReporter(junitPath))
	ginkgo.RunSpecsWithDefaultAndCustomReporters(t, "Submariner E2E suite", reporterList)
}
