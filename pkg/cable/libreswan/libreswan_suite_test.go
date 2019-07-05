package libreswan_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestLibreswan(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Libreswan Suite")
}
