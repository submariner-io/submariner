package cableengine

import (
	"github.com/rancher/submariner/pkg/types"
)

type Engine interface {
	StartEngine(ignition bool) error
	InstallCable(types.SubmarinerEndpoint) error
	RemoveCable(string) error
}
