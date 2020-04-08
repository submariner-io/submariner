package v1

import (
	"github.com/submariner-io/submariner/pkg/cable"
)

const HAStatusActive = "ACTIVE"
const HAStatusPassive = "PASSIVE"

type GatewayStatus struct {
	Version     string             `json:"version"`
	HAStatus    string             `json:"haStatus"`
	Host        string             `json:"host"`
	Connections []cable.Connection `json:"connections"`
}
