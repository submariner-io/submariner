package cable

type ConnectionStatus string

const (
	Established     ConnectionStatus = "ESTABLISHED"
	Connecting      ConnectionStatus = "CONNECTING"
	ConnectionError ConnectionStatus = "ERROR"
	Unreachable     ConnectionStatus = "UNREACHABLE"
	AuthError       ConnectionStatus = "AUTH ERROR"
)

type Connection struct {
	CableName     string           `json:"cableName"`
	ClusterID     string           `json:"clusterID"`
	Status        ConnectionStatus `json:"status"`
	RemoteIP      string           `json:"remoteIP"`
	LocalIP       string           `json:"localIP"`
	CableDriver   string           `json:"cableDriver"`
	RemoteSubnets []string         `json:"remoteSubnets"`
}
