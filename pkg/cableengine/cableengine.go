package cableengine

import (
	"github.com/submariner-io/submariner/pkg/types"
)

type Engine interface {
	StartEngine() error
	InstallCable(types.SubmarinerEndpoint) error
	RemoveCable(string) error
}
