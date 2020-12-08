package healthchecker

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestHealthChecker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Health Checker Suite")
}
