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

package logger

import (
	k8sV1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
)

type Handler struct {
	event.HandlerBase
}

func NewHandler() event.Handler {
	return &Handler{}
}

func (l *Handler) GetName() string {
	return "logger"
}

func (l *Handler) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (l *Handler) TransitionToNonGateway() error {
	klog.V(log.DEBUG).Info("The current node is no longer a Gateway")
	return nil
}

func (l *Handler) TransitionToGateway() error {
	klog.V(log.DEBUG).Info("The current node has become a Gateway")
	return nil
}

func (l *Handler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for the local cluster has been created: %#v", endpoint.Spec)
	return nil
}

func (l *Handler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("The Endpoint for the local cluster has been updated: %#v", endpoint.Spec)
	return nil
}

func (l *Handler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("The Endpoint for the local cluster has been removed: %#v", endpoint.Spec)
	return nil
}

func (l *Handler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been created: %#v",
		endpoint.Spec.ClusterID, endpoint.Spec)
	return nil
}

func (l *Handler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been updated: %#v",
		endpoint.Spec.ClusterID, endpoint.Spec)
	return nil
}

func (l *Handler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been removed: %#v",
		endpoint.Spec.ClusterID, endpoint.Spec)
	return nil
}

func (l *Handler) NodeCreated(node *k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q and addresses %#v has been added to the cluster",
		node.Name, node.Status.Addresses)
	return nil
}

func (l *Handler) NodeUpdated(node *k8sV1.Node) error {
	klog.V(log.TRACE).Infof("A Node with name %q and addresses %#v has been updated",
		node.Name, node.Status.Addresses)
	return nil
}

func (l *Handler) NodeRemoved(node *k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q has been removed",
		node.Name)
	return nil
}
