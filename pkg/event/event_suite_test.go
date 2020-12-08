package event_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestEventPackage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Event suite")
}
