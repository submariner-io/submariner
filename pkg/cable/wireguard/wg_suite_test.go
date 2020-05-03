package wireguard

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestWireGuard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WireGuard Suite")
}
