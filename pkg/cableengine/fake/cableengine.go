package fake

import (
	"sync"

	. "github.com/onsi/gomega"
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
	installCable              chan *v1.EndpointSpec
	ErrOnInstallCable         error
	removeCable               chan *v1.EndpointSpec
	ErrOnRemoveCable          error
}

var _ cableengine.Engine = &Engine{}

func New() *Engine {
	return &Engine{
		HAStatus:     v1.HAStatusPassive,
		Connections:  []v1.Connection{},
		installCable: make(chan *v1.EndpointSpec, 100),
		removeCable:  make(chan *v1.EndpointSpec, 100),
	}
}

func (e *Engine) StartEngine() error {
	return nil
}

func (e *Engine) InstallCable(endpoint types.SubmarinerEndpoint) error {
	err := e.ErrOnInstallCable
	if err != nil {
		e.ErrOnInstallCable = nil
		return err
	}

	e.installCable <- &endpoint.Spec
	return nil
}

func (e *Engine) RemoveCable(endpoint types.SubmarinerEndpoint) error {
	err := e.ErrOnRemoveCable
	if err != nil {
		e.ErrOnRemoveCable = nil
		return err
	}

	e.removeCable <- &endpoint.Spec
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

func (e *Engine) VerifyInstallCable(expected *v1.EndpointSpec) {
	Eventually(e.installCable, 5).Should(Receive(Equal(expected)), "InstallCable was not invoked")
}

func (e *Engine) VerifyRemoveCable(expected *v1.EndpointSpec) {
	Eventually(e.removeCable, 5).Should(Receive(Equal(expected)), "RemoveCable was not invoked")
}
