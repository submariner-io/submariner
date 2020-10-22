package ovn

import "github.com/submariner-io/submariner/pkg/event"

type SyncHandler struct {
	event.HandlerBase
}

func NewSyncHandler() *SyncHandler {
	return &SyncHandler{}
}

func (ovn *SyncHandler) GetName() string {
	return "ovn-sync-handler"
}

func (ovn *SyncHandler) GetNetworkPlugin() string {
	return "OVNKubernetes"
}
