package e2e

import (
	"testing"

	"github.com/rancher/submariner/test/e2e/framework"

	_ "github.com/rancher/submariner/test/e2e/example"
	_ "github.com/rancher/submariner/test/e2e/dataplane"
)

func init() {
	framework.ParseFlags()
}

func TestE2E(t *testing.T) {

	RunE2ETests(t)
}
