package cable

import v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"

type ConnectionStatus string

const (
	Connected       ConnectionStatus = "CONNECTED"
	Connecting      ConnectionStatus = "CONNECTING"
	ConnectionError ConnectionStatus = "ERROR"
)

// Connection structure represents the status of an existing cable connection,
// it includes details the endpoint details, and it's meant to be exposed
// through an HTTP API on the submariner-engine.
type Connection struct {
	Status        ConnectionStatus `json:"status"`
	StatusMessage string           `json:"statusMessage"`
	Endpoint      v1.EndpointSpec  `json:"endpoint"`
}
