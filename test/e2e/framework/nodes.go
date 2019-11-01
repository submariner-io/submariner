package framework

import (
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (f *Framework) FindNodes(cluster ClusterIndex) (gateway *v1.Node, nonGateway *v1.Node) {
	nodes := AwaitUntil("list nodes", func() (interface{}, error) {
		// Ignore the control plane node labeled as master as it doesn't allow scheduling of pods
		return f.ClusterClients[cluster].CoreV1().Nodes().List(metav1.ListOptions{
			LabelSelector: "!node-role.kubernetes.io/master",
		})
	}, NoopCheckResult).(*v1.NodeList)

	var gatewayName, nonGatewayName string
	for i := range nodes.Items {
		if nodes.Items[i].Labels[GatewayLabel] == "true" {
			gateway = &nodes.Items[i]
			gatewayName = gateway.Name
		} else {
			nonGateway = &nodes.Items[i]
			nonGatewayName = nonGateway.Name
		}
	}

	Logf("Found gateway node: %s, non-gateway node: %s", gatewayName, nonGatewayName)
	return
}

func (f *Framework) SetGatewayLabelOnNode(cluster ClusterIndex, nodeName string, isGateway bool) {
	// Escape the '/' char in the label name with the special sequence "~1" so it isn't treated as part of the path
	DoPatchOperation("/metadata/labels/"+strings.Replace(GatewayLabel, "/", "~1", -1), strconv.FormatBool(isGateway),
		func(pt types.PatchType, payload []byte) error {
			_, err := f.ClusterClients[cluster].CoreV1().Nodes().Patch(nodeName, pt, payload)
			return err
		})
}
