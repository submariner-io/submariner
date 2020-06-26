package fake

import (
	"sync"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/types"
)

type Engine struct {
	sync.Mutex
	Connections               []v1.Connection
	ListCableConnectionsError error
	HAStatus                  v1.HAStatus
	LocalEndPoint             *types.SubmarinerEndpoint
}

var _ cableengine.Engine = &Engine{}

func New() *Engine {
	return &Engine{
		HAStatus:    v1.HAStatusPassive,
		Connections: []v1.Connection{},
	}
}

func (e *Engine) StartEngine() error {
	return nil
}

func (e *Engine) InstallCable(remote types.SubmarinerEndpoint) error {
	return nil
}

func (e *Engine) RemoveCable(remote types.SubmarinerEndpoint) error {
	return nil
}

func (e *Engine) GetLocalEndpoint() *types.SubmarinerEndpoint {
	return e.LocalEndPoint
}

func (e *Engine) ListCableConnections() (*[]v1.Connection, error) {
	e.Lock()
	defer e.Unlock()

	if e.ListCableConnectionsError != nil {
		return nil, e.ListCableConnectionsError
	}

	if e.Connections == nil {
		return nil, nil
	}

	return &e.Connections, nil
}

func (e *Engine) GetHAStatus() v1.HAStatus {
	e.Lock()
	defer e.Unlock()

	return e.HAStatus
}
