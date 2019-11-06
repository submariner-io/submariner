package framework

import (
	"k8s.io/apimachinery/pkg/api/errors"
)

// identify API errors which could be considered transient/recoverable
// due to server state.
func IsTransientError(err error, opMsg string) bool {
	if errors.IsInternalError(err) ||
		errors.IsServerTimeout(err) ||
		errors.IsTimeout(err) ||
		errors.IsServiceUnavailable(err) ||
		errors.IsUnexpectedServerError(err) ||
		errors.IsTooManyRequests(err) {

		Logf("Transient failure when attempting to %s: %v", opMsg, err)
		return true
	}

	return false
}
