package framework

import (
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// FindNodesByGatewayLabel finds the nodes in a given cluster by matching 'submariner.io/gateway' value. A missing label
// is treated as false. Note the control plane node labeled as master is ignored.
func (f *Framework) FindNodesByGatewayLabel(cluster ClusterIndex, isGateway bool) []*v1.Node {
	nodes := AwaitUntil("list nodes", func() (interface{}, error) {
		// Ignore the control plane node labeled as master as it doesn't allow scheduling of pods
		return f.ClusterClients[cluster].CoreV1().Nodes().List(metav1.ListOptions{
			LabelSelector: "!node-role.kubernetes.io/master",
		})
	}, NoopCheckResult).(*v1.NodeList)

	expLabelValue := strconv.FormatBool(isGateway)
	retNodes := []*v1.Node{}
	for i := range nodes.Items {
		value, exists := nodes.Items[i].Labels[GatewayLabel]
		if !exists {
			value = "false"
		}

		if value == expLabelValue {
			retNodes = append(retNodes, &nodes.Items[i])
		}
	}

	return retNodes
}

// SetGatewayLabelOnNode sets the 'submariner.io/gateway' value for a node to the specified value.
func (f *Framework) SetGatewayLabelOnNode(cluster ClusterIndex, nodeName string, isGateway bool) {
	// Escape the '/' char in the label name with the special sequence "~1" so it isn't treated as part of the path
	PatchString("/metadata/labels/"+strings.Replace(GatewayLabel, "/", "~1", -1), strconv.FormatBool(isGateway),
		func(pt types.PatchType, payload []byte) error {
			_, err := f.ClusterClients[cluster].CoreV1().Nodes().Patch(nodeName, pt, payload)
			return err
		})
}
