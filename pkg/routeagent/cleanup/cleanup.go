package cleanup

type Handler interface {
	GetName() string
	GatewayToNonGatewayTransition() error
}
