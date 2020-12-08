package ovn

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestOvn(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OVN Test Suite")
}
