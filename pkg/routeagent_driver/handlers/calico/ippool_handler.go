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

package calico

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	calicoapi "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	calicocs "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	"github.com/submariner-io/admiral/pkg/log"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	errorutils "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	SubmarinerIPPool = "submariner.io/ippool"
)

type calicoIPPoolHandler struct {
	event.HandlerBase
	restConfig *rest.Config
	client     calicocs.Interface
}

var NewClient = func(restConfig *rest.Config) (calicocs.Interface, error) {
	return calicocs.NewForConfig(restConfig) //nolint:wrapcheck // No need to wrap
}

var logger = log.Logger{Logger: logf.Log.WithName("CalicoIPPool")}

func NewCalicoIPPoolHandler(restConfig *rest.Config) event.Handler {
	return &calicoIPPoolHandler{
		restConfig: restConfig,
	}
}

func (h *calicoIPPoolHandler) GetNetworkPlugins() []string {
	return []string{cni.Calico}
}

func (h *calicoIPPoolHandler) GetName() string {
	return "Calico IPPool handler"
}

func (h *calicoIPPoolHandler) Init() error {
	var err error

	h.client, err = NewClient(h.restConfig)

	return errors.Wrap(err, "error initializing Calico clientset")
}

func (h *calicoIPPoolHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	if !h.State().IsOnGateway() {
		logger.V(log.TRACE).Info("Ignore RemoteEndpointCreated event (node isn't Gateway)")
		return nil
	}

	err := h.createIPPool(endpoint)

	return errors.Wrap(err, "failed to handle RemoteEndpointCreated event")
}

func (h *calicoIPPoolHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	if !h.State().IsOnGateway() {
		logger.V(log.TRACE).Info("Ignore RemoteEndpointRemoved event (node isn't Gateway)")
		return nil
	}

	err := h.deleteIPPool(endpoint)

	return errors.Wrap(err, "failed to handle RemoteEndpointRemoved event")
}

func (h *calicoIPPoolHandler) TransitionToGateway() error {
	var retErrors []error
	logger.Info("TransitionToGateway")

	endpoints := h.State().GetRemoteEndpoints()
	for i := range endpoints {
		err := h.createIPPool(&endpoints[i])
		if err != nil {
			logger.Warningf("Failed to create ippool %s", endpoints[i].GetName())
			retErrors = append(retErrors,
				errors.Wrapf(err, "error creating Calico IPPool for endpoint %q ", endpoints[i].GetName()))
		}
	}

	return errorutils.NewAggregate(retErrors)
}

func (h *calicoIPPoolHandler) Uninstall() error {
	logger.Info("Uninstalling Calico IPPools used for Submariner")

	labelSelector := labels.SelectorFromSet(map[string]string{SubmarinerIPPool: "true"}).String()
	err := h.client.ProjectcalicoV3().IPPools().DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: labelSelector})

	if err != nil && !apierrors.IsNotFound(err) {
		return errors.Wrapf(err, "Failed to delete Calico IPPools using labelSelector %q", labelSelector)
	}

	logger.Infof("Successfully delete Calico IPPools using labelSelector %q", labelSelector)

	return nil
}

func (h *calicoIPPoolHandler) createIPPool(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractIPv4Subnets(endpoint.Spec.Subnets)
	var retErrors []error

	for _, subnet := range subnets {
		iPPoolObj := &calicoapi.IPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:   getEndpointSubnetIPPoolName(endpoint, subnet),
				Labels: map[string]string{SubmarinerIPPool: "true"},
			},
			Spec: calicoapi.IPPoolSpec{
				CIDR:        subnet,
				NATOutgoing: false,
				Disabled:    true,
			},
		}
		_, err := h.client.ProjectcalicoV3().IPPools().Create(context.TODO(), iPPoolObj, metav1.CreateOptions{})

		if err == nil {
			logger.Infof("Successfully created Calico IPPool %q", iPPoolObj.GetName())
			continue
		}

		if !apierrors.IsAlreadyExists(err) {
			retErrors = append(retErrors,
				errors.Wrapf(err, "error creating Calico IPPool for ClusterID %q subnet %q (is Calico API server running?)",
					endpoint.Spec.ClusterID, subnet))
		}
	}

	return errorutils.NewAggregate(retErrors)
}

func (h *calicoIPPoolHandler) deleteIPPool(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractIPv4Subnets(endpoint.Spec.Subnets)
	var retErrors []error

	for _, subnet := range subnets {
		poolName := getEndpointSubnetIPPoolName(endpoint, subnet)

		err := h.client.ProjectcalicoV3().IPPools().Delete(context.TODO(),
			poolName, metav1.DeleteOptions{})

		if err == nil {
			logger.Infof("Successfully deleted Calico IPPool %q", poolName)
			continue
		}

		if !apierrors.IsNotFound(err) {
			retErrors = append(retErrors,
				errors.Wrapf(err, "error deleting Calico IPPool for ClusterID %q subnet %q (is Calico API server running?)",
					endpoint.Spec.ClusterID, subnet))
		}
	}

	return errorutils.NewAggregate(retErrors)
}

func getEndpointSubnetIPPoolName(endpoint *submV1.Endpoint, subnet string) string {
	return fmt.Sprintf("submariner-%s-%s", endpoint.Spec.ClusterID, strings.ReplaceAll(subnet, "/", "-"))
}
