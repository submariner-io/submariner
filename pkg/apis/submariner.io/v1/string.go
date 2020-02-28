package v1

import (
	"encoding/json"
	"fmt"
)

// String method returns a string representation, for human readability.
func (e *Endpoint) String() string {

	bytes, err := json.Marshal(e)
	if err != nil {
		return fmt.Sprintf("%#v", e)
	}
	return string(bytes)
}
