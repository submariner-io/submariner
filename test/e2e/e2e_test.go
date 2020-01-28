package e2e

import (
	"testing"

	"github.com/submariner-io/submariner/test/e2e/framework"

	_ "github.com/submariner-io/submariner/test/e2e/cluster"
	_ "github.com/submariner-io/submariner/test/e2e/dataplane"
	_ "github.com/submariner-io/submariner/test/e2e/example"
	_ "github.com/submariner-io/submariner/test/e2e/redundancy"
)

func init() {
	framework.ParseFlags()
}

func TestE2E(t *testing.T) {

	RunE2ETests(t)
}
