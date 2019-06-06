package cableengine

import (
	"github.com/rancher/submariner/pkg/types"
)

type CableEngine interface {
	StartEngine(ignition bool) error
	ReloadEngine() error
	StopEngine() error
	InstallCable(types.SubmarinerEndpoint) error
	RemoveCable(string) error
	SyncCables(string, []types.SubmarinerEndpoint) error
}
