package environment

type Specification struct {
	ClusterID   string
	Namespace   string
	ClusterCidr []string
	ServiceCidr []string
}
