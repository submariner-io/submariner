package wireguard

import (
	"fmt"

	"github.com/submariner-io/submariner/pkg/cable"
)

func (w *wireguard) GetConnections() (*[]cable.Connection, error) {
	return nil, fmt.Errorf("Not implemented yet")
}
