package e2e

import (
	"testing"

	"github.com/submariner-io/shipyard/test/e2e"
	_ "github.com/submariner-io/submariner/test/e2e/cluster"
	_ "github.com/submariner-io/submariner/test/e2e/dataplane"
	_ "github.com/submariner-io/submariner/test/e2e/redundancy"
)

func TestE2E(t *testing.T) {
	e2e.RunE2ETests(t)
}
