package cable

import (
	"fmt"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

type ConnectionStatus string

const (
	Connected       ConnectionStatus = "CONNECTED"
	Connecting      ConnectionStatus = "CONNECTING"
	ConnectionError ConnectionStatus = "ERROR"
)

// Connection structure represents the state of an existing cable connection
// including the Endpoint details. It is meant to be exposed
// through an HTTP API on the submariner-engine.
type Connection struct {
	Status        ConnectionStatus `json:"status"`
	StatusMessage string           `json:"statusMessage"`
	Endpoint      v1.EndpointSpec  `json:"endpoint"`
}

func NewConnection(endpointSpec v1.EndpointSpec) *Connection {
	return &Connection{Endpoint: endpointSpec}
}

func (c *Connection) SetStatus(status ConnectionStatus, messageFormat string, a ...interface{}) {
	c.Status = status
	c.StatusMessage = fmt.Sprintf(messageFormat, a...)
}
