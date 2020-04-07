package ipsec_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestIpsec(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ipsec Suite")
}
