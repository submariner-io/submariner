package gomega

import (
	"fmt"
	"strings"

	"github.com/onsi/gomega/format"
	gomegaTypes "github.com/onsi/gomega/types"
)

type containErrorSubstring struct {
	expected error
}

func ContainErrorSubstring(expected error) gomegaTypes.GomegaMatcher {
	return &containErrorSubstring{expected}
}

func (m *containErrorSubstring) Match(x interface{}) (bool, error) {
	actual, ok := x.(error)
	if !ok {
		return false, fmt.Errorf("ContainErrorSubstring matcher requires an error.  Got:\n%s", format.Object(x, 1))
	}
	return strings.Contains(actual.Error(), m.expected.Error()), nil
}

func (m *containErrorSubstring) FailureMessage(actual interface{}) string {
	return format.Message(actual, "to contain substring", m.expected.Error())
}

func (m *containErrorSubstring) NegatedFailureMessage(actual interface{}) (message string) {
	return format.Message(actual, "not to contain substring", m.expected.Error())
}
