package framework

import (
	"k8s.io/apimachinery/pkg/api/errors"
)

// identify API errors which could be considered transient/recoverable
// due to server state.
func IsTransientError(err error) bool {
	return errors.IsInternalError(err) ||
		errors.IsServerTimeout(err) ||
		errors.IsTimeout(err) ||
		errors.IsServiceUnavailable(err) ||
		errors.IsUnexpectedServerError(err) ||
		errors.IsTooManyRequests(err)
}
