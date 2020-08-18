package route

const (
	// IPTable chains used by Globalnet Controller
	SmGlobalnetIngressChain = "SUBMARINER-GN-INGRESS"
	SmGlobalnetEgressChain  = "SUBMARINER-GN-EGRESS"
	SmGlobalnetMarkChain    = "SUBMARINER-GN-MARK"

	// IPTable chains used by RouteAgent
	SmPostRoutingChain = "SUBMARINER-POSTROUTING"
)
