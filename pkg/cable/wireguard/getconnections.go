package wireguard

import (
	"fmt"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

func (w *wireguard) GetConnections() (*[]v1.Connection, error) {
	return nil, fmt.Errorf("Not implemented yet")
}
