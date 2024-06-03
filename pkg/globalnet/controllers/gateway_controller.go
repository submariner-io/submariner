/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	admUtil "github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/packetfilter"
	"github.com/submariner-io/submariner/pkg/ipam"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

func NewGatewayController(config *syncer.ResourceSyncerConfig, informer cache.SharedInformer, pool *ipam.IPPool, hostName,
	namespace, cniIP string,
) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	logger.Info("Creating Gateway controller")

	pfIface, err := packetfilter.New()
	if err != nil {
		return nil, errors.Wrap(err, "error creating the PacketFilter Interface handler")
	}

	controller := &gatewayController{
		baseIPAllocationController: newBaseIPAllocationController(pool, pfIface),
		hostName:                   hostName,
		cniIP:                      cniIP,
	}

	config = NewGatewayResourceSyncerConfig(config, namespace)

	config.Federator = federate.NewUpdateFederator(config.SourceClient, config.RestMapper, namespace,
		func(oldObj *unstructured.Unstructured, newObj *unstructured.Unstructured) *unstructured.Unstructured {
			return updateGlobalIPAnnotation(oldObj, newObj.GetAnnotations()[constants.SmGlobalIP]).(*unstructured.Unstructured)
		})

	config.Transform = controller.process
	config.Name = "Gateway syncer"

	controller.resourceSyncer, err = syncer.NewResourceSyncerWithSharedInformer(config, informer)
	if err != nil {
		return nil, errors.Wrap(err, "error creating the resource syncer")
	}

	_, gvr, err := admUtil.ToUnstructuredResource(&submarinerv1.Gateway{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	gatewayClient := config.SourceClient.Resource(*gvr).Namespace(namespace)

	// Gateways retain their allocated global IPs regardless of their HA status (active or passive) so reserve the global IPs
	// for all the Gateways.

	gateways, err := gatewayClient.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "error listing Gateways")
	}

	for i := range gateways.Items {
		if err := controller.reserveAllocatedIP(config.Federator, &gateways.Items[i]); err != nil {
			return nil, err
		}
	}

	return controller, nil
}

func NewGatewayResourceSyncerConfig(base *syncer.ResourceSyncerConfig, namespace string) *syncer.ResourceSyncerConfig {
	return &syncer.ResourceSyncerConfig{
		ResourceType:    &submarinerv1.Gateway{},
		SourceClient:    base.SourceClient,
		SourceNamespace: namespace,
		RestMapper:      base.RestMapper,
		Scheme:          base.Scheme,
		ResourcesEquivalent: func(_, _ *unstructured.Unstructured) bool {
			return true // we don't need to handle updates
		},
	}
}

func (n *gatewayController) process(from runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	gateway := from.(*submarinerv1.Gateway)

	if op == syncer.Delete {
		// Gateway deleted - here we just release its global IP, if any. If it's the local Gateway then the other Gateway controller
		// started by the gatewayMonitor will remove the ingress rules in parallel.
		if existingGlobalIP := gateway.GetAnnotations()[constants.SmGlobalIP]; existingGlobalIP != "" {
			logger.Infof(" Gateway %q deleted - releasing its global IP %q", gateway.Name, existingGlobalIP)

			_ = n.pool.Release(existingGlobalIP)
		}

		return nil, false
	}

	if gateway.Name != n.hostName {
		return nil, false
	}

	logger.Infof("Processing %sd local Gateway %q", op, gateway.Name)

	return n.allocateIP(gateway)
}

func (n *gatewayController) allocateIP(gateway *submarinerv1.Gateway) (runtime.Object, bool) {
	if n.cniIP == "" {
		// To support connectivity from HostNetwork to remote clusters, globalnet requires the CNI IP of the local node.
		return nil, false
	}

	globalIP := gateway.GetAnnotations()[constants.SmGlobalIP]
	if globalIP != "" {
		return nil, false
	}

	ips, err := n.pool.Allocate(1)
	if err != nil {
		logger.Errorf(err, "Error allocating IPs for Gateway %q", gateway.Name)
		return nil, true
	}

	globalIP = ips[0]

	logger.Infof("Allocated global IP %s for Gateway %q", globalIP, gateway.Name)

	logger.Infof("Adding ingress rules for Gateway %q with global IP %s, CNI IP %s", gateway.Name, globalIP, n.cniIP)

	if err := n.pfIface.AddIngressRulesForHealthCheck(n.cniIP, globalIP); err != nil {
		logger.Errorf(err, "Error programming ingress rules for Gateway %q", gateway.Name)

		_ = n.pool.Release(globalIP)

		return nil, true
	}

	return updateGlobalIPAnnotation(gateway, globalIP), false
}

func (n *gatewayController) reserveAllocatedIP(federator federate.Federator, obj *unstructured.Unstructured) error {
	existingGlobalIP := obj.GetAnnotations()[constants.SmGlobalIP]
	if existingGlobalIP == "" {
		return nil
	}

	if n.cniIP == "" {
		return nil
	}

	// If this is not the Gateway on the local host then just reserve its global IP.

	err := n.pool.Reserve(existingGlobalIP)
	if err == nil && obj.GetName() == n.hostName {
		err = n.pfIface.AddIngressRulesForHealthCheck(n.cniIP, existingGlobalIP)
		if err != nil {
			_ = n.pool.Release(existingGlobalIP)
		}
	}

	if err != nil {
		logger.Warningf("Could not reserve allocated GlobalIP for Gateway %q: %v", obj.GetName(), err)

		// If this is not the Gateway on the local host/node then leave the annotation as is since we're not able to remove the
		// ingress rules from another node. We'll let the globalnet process running on that host take care of it.

		if obj.GetName() != n.hostName {
			return nil
		}

		if err := n.pfIface.RemoveIngressRulesForHealthCheck(n.cniIP, existingGlobalIP); err != nil {
			logger.Errorf(err, "Error deleting rules for Gateway %q", n.hostName)
		}

		return errors.Wrap(federator.Distribute(context.TODO(), updateGlobalIPAnnotation(obj, "")),
			"error updating the Gateway global IP annotation")
	}

	logger.Infof("Successfully reserved allocated GlobalIP %q for Gateway %q", existingGlobalIP, obj.GetName())

	return nil
}

func updateGlobalIPAnnotation(obj runtime.Object, globalIP string) runtime.Object {
	objMeta, _ := meta.Accessor(obj)

	annotations := objMeta.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	if globalIP == "" {
		delete(annotations, constants.SmGlobalIP)
	} else {
		annotations[constants.SmGlobalIP] = globalIP
	}

	objMeta.SetAnnotations(annotations)

	return obj
}
