package datastoresyncer

import (
	"github.com/submariner-io/admiral/pkg/log"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

func (d *DatastoreSyncer) handleCreateOrUpdateNode(obj runtime.Object) bool {
	node := obj.(*k8sv1.Node)
	if node.Name != d.localNodeName {
		return false
	}

	globalIpOfNode := node.GetAnnotations()[constants.SubmarinerIpamGlobalIp]

	return d.updateLocalEndpointIfNecessary(globalIpOfNode)
}

func (d *DatastoreSyncer) isNodeEquivalent(obj1, obj2 *unstructured.Unstructured) bool {
	if obj1.GetName() != d.localNodeName {
		// Ignore this event. We are only interested in active GatewayNode events.
		return true
	}

	existingGlobalIP := obj1.GetAnnotations()[constants.SubmarinerIpamGlobalIp]
	newGlobalIP := obj2.GetAnnotations()[constants.SubmarinerIpamGlobalIp]

	if existingGlobalIP == newGlobalIP {
		klog.V(log.DEBUG).Infof("isNodeEquivalent called for %q, existingGlobalIP %q, newGlobalIP %q",
			obj1.GetName(), existingGlobalIP, newGlobalIP)
		return true
	}

	return false
}

func (d *DatastoreSyncer) updateLocalEndpointIfNecessary(globalIpOfNode string) bool {
	if globalIpOfNode != "" && d.localFederator != nil && d.localEndpoint.Spec.HealthCheckIP != globalIpOfNode {
		klog.Infof("Updating the endpoint HealthCheckIP to globalIP %q", globalIpOfNode)

		d.localEndpoint.Spec.HealthCheckIP = globalIpOfNode
		if err := d.createOrUpdateLocalEndpoint(d.localFederator); err != nil {
			klog.Warningf("error updating the local submariner Endpoint with HealthcheckIP: %v", err)
			return true
		}
	}

	return false
}
