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
	"fmt"
	"os"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	admUtil "github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/iptables"
	"github.com/submariner-io/submariner/pkg/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
)

func NewGatewayMonitor(spec Specification, localCIDRs []string, config *watcher.Config) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	gatewayMonitor := &gatewayMonitor{
		baseController: newBaseController(),
		spec:           spec,
		localSubnets:   stringset.New(localCIDRs...).Elements(),
		remoteSubnets:  stringset.NewSynchronized(),
	}

	var err error

	gatewayMonitor.ipt, err = iptables.New()
	if err != nil {
		return nil, errors.Wrap(err, "error creating IP tables")
	}

	if config.RestMapper == nil {
		if config.RestMapper, err = admUtil.BuildRestMapper(config.RestConfig); err != nil {
			return nil, errors.Wrap(err, "error creating the RestMapper")
		}
	}

	if config.Client == nil {
		if config.Client, err = dynamic.NewForConfig(config.RestConfig); err != nil {
			return nil, errors.Wrap(err, "error creating dynamic client")
		}
	}

	if config.Scheme == nil {
		config.Scheme = scheme.Scheme
	}

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "IPAM GatewayMonitor",
			ResourceType: &submarinerv1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: gatewayMonitor.handleCreatedOrUpdatedEndpoint,
				OnUpdateFunc: gatewayMonitor.handleCreatedOrUpdatedEndpoint,
				OnDeleteFunc: gatewayMonitor.handleRemovedEndpoint,
			},
			SourceNamespace: spec.Namespace,
		},
	}

	gatewayMonitor.endpointWatcher, err = watcher.New(config)
	if err != nil {
		return nil, errors.Wrap(err, "error creating the Endpoint watcher")
	}

	gatewayMonitor.syncerConfig = &syncer.ResourceSyncerConfig{
		SourceClient:    config.Client,
		SourceNamespace: corev1.NamespaceAll,
		Direction:       syncer.RemoteToLocal,
		RestMapper:      config.RestMapper,
		Federator:       broker.NewFederator(config.Client, config.RestMapper, corev1.NamespaceAll, ""),
		Scheme:          config.Scheme,
	}

	return gatewayMonitor, nil
}

func (g *gatewayMonitor) Start() error {
	klog.Info("Starting GatewayMonitor to monitor the active Gateway node in the cluster.")

	err := g.endpointWatcher.Start(g.stopCh)
	if err != nil {
		return errors.Wrap(err, "error starting the Endpoint watcher")
	}

	return nil
}

func (g *gatewayMonitor) Stop() {
	klog.Info("GatewayMonitor stopping")

	g.baseController.Stop()

	g.syncMutex.Lock()
	g.stopControllers()
	g.syncMutex.Unlock()
}

func (g *gatewayMonitor) handleCreatedOrUpdatedEndpoint(obj runtime.Object, numRequeues int) bool {
	endpoint := obj.(*submarinerv1.Endpoint)

	klog.V(log.DEBUG).Infof("In processNextEndpoint, endpoint info: %+v", endpoint)

	// For each new local endpoint we need to create a new ClusterGlobalEgressIP object
	if endpoint.Spec.ClusterID != g.spec.ClusterID {
		klog.V(log.DEBUG).Infof("Endpoint %q, host: %q belongs to a remote cluster",
			endpoint.Spec.ClusterID, endpoint.Spec.Hostname)

		overlap, err := cidr.IsOverlapping(endpoint.Spec.Subnets, g.spec.GlobalCIDR[0])
		if err != nil {
			// Ideally this case will never hit, as the subnets are valid CIDRs
			klog.Warningf("unable to validate overlapping Service CIDR: %s", err)
		}

		if overlap {
			// When GlobalNet is used, globalCIDRs allocated to the clusters should not overlap.
			// If they overlap, skip the endpoint as its an invalid configuration which is not supported.
			klog.Errorf("GlobalCIDR %q of local cluster %q overlaps with remote cluster %s",
				g.spec.GlobalCIDR[0], g.spec.ClusterID, endpoint.Spec.ClusterID)

			return false
		}

		for _, remoteSubnet := range endpoint.Spec.Subnets {
			g.remoteSubnets.Add(remoteSubnet)
		}

		return false
	}

	klog.V(log.DEBUG).Infof("Endpoint %q, host: %q belongs to a local cluster",
		endpoint.Spec.ClusterID, endpoint.Spec.Hostname)

	// Create clusterGlobalEgressIP for each local endpoint
	numberOfIPs := DefaultNumberOfClusterEgressIPs
	defaultEgressIP := &submarinerv1.ClusterGlobalEgressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", util.EnsureValidName(endpoint.Spec.Hostname), constants.ClusterGlobalEgressIPName),
		},
		Spec: submarinerv1.ClusterGlobalEgressIPSpec{
			NumberOfIPs: &numberOfIPs,
		},
	}

	defaultEgressIPObj, gvr, err := admUtil.ToUnstructuredResource(defaultEgressIP, g.syncerConfig.RestMapper)
	if err != nil {
		klog.Errorf("error converting resource %v", err)
		return false
	}

	client := g.syncerConfig.SourceClient.Resource(*gvr)

	_, err = client.Get(context.TODO(), defaultEgressIP.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.Infof("Creating ClusterGlobalEgressIP resource %q for node %s", defaultEgressIP.Name, endpoint.Spec.Hostname)

		_, err = client.Create(context.TODO(), defaultEgressIPObj, metav1.CreateOptions{})
		if err != nil {
			klog.Errorf("error creating ClusterGlobalEgressIP resource %s for node %s", defaultEgressIP.Name, endpoint.Spec.Hostname)
			return false
		}
	} else if err != nil {
		klog.Errorf("error retreving ClusterGlobalEgressIP resource %s for node %s", defaultEgressIP.Name, endpoint.Spec.Hostname)
		return false
	}

	// If the endpoint hostname matches with our hostname, it implies we are on gateway node

	klog.V(log.DEBUG).Infof("Starting GlobalNet ControlPlane Controllers %s", endpoint.Spec.PrivateIP)

	g.syncMutex.Lock()

	err = g.startControllers()
	if err != nil {
		klog.Fatalf("Error starting the controllers: %v", err)
	}

	g.syncMutex.Unlock()

	// Only stop controllers on endpoint deletion since we can have multiple gateways
	// else {
	// 	klog.V(log.DEBUG).Infof("Transitioned to non-gateway node with endpoint private IP %s", endpoint.Spec.PrivateIP)

	// 	g.syncMutex.Lock()
	// 	if g.isGatewayNode {
	// 		g.stopControllers()
	// 		g.isGatewayNode = false
	// 	}
	// 	g.syncMutex.Unlock()
	// }

	return false
}

func (g *gatewayMonitor) handleRemovedEndpoint(obj runtime.Object, numRequeues int) bool {
	endpoint := obj.(*submarinerv1.Endpoint)

	klog.V(log.DEBUG).Infof("Informed of removed endpoint for gateway monitor: %v", endpoint)

	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Could not retrieve hostname: %v", err)
	}

	if endpoint.Spec.Hostname == hostname && endpoint.Spec.ClusterID == g.spec.ClusterID {
		// Remove clusterGlobalEgressIP for endpoint
		clusterGlobalEgressIPName := fmt.Sprintf("%s-%s", util.EnsureValidName(endpoint.Spec.Hostname), constants.ClusterGlobalEgressIPName)

		_, gvr, err := admUtil.ToUnstructuredResource(&submarinerv1.ClusterGlobalEgressIP{}, g.syncerConfig.RestMapper)
		if err != nil {
			klog.Errorf("error converting resource %v", err)
			return false
		}

		client := g.syncerConfig.SourceClient.Resource(*gvr)

		err = client.Delete(context.TODO(), clusterGlobalEgressIPName, metav1.DeleteOptions{})
		if err != nil {
			klog.Errorf("error creating ClusterGlobalEgressIP resource %s for node %s", clusterGlobalEgressIPName, endpoint.Spec.Hostname)
			return false
		}
	} else if endpoint.Spec.ClusterID != g.spec.ClusterID {
		// Endpoint associated with remote cluster is removed, delete the associated flows.
		for _, remoteSubnet := range endpoint.Spec.Subnets {
			g.remoteSubnets.Remove(remoteSubnet)
		}
	}

	return false
}

func (g *gatewayMonitor) startControllers() error {
	klog.Infof("Starting Globalnet controllers")

	pool, err := ipam.NewIPPool(g.spec.GlobalCIDR[0])
	if err != nil {
		return errors.Wrap(err, "error creating the IP pool")
	}

	g.controllers = nil

	c, err := NewNodeController(g.syncerConfig, pool)
	if err != nil {
		return errors.Wrap(err, "error creating the Node controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewClusterGlobalEgressIPController(g.syncerConfig, g.localSubnets, pool)
	if err != nil {
		return errors.Wrap(err, "error creating the ClusterGlobalEgressIP controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewGlobalEgressIPController(g.syncerConfig, pool)
	if err != nil {
		return errors.Wrap(err, "error creating the GlobalEgressIP controller")
	}

	g.controllers = append(g.controllers, c)

	// The GlobalIngressIP controller needs to be started before the ServiceExport and Service controllers to ensure
	// reconciliation works properly.
	c, err = NewGlobalIngressIPController(g.syncerConfig, pool)
	if err != nil {
		return errors.Wrap(err, "error creating the GlobalIngressIP controller")
	}

	g.controllers = append(g.controllers, c)

	podControllers, err := NewIngressPodControllers(g.syncerConfig)
	if err != nil {
		return errors.Wrap(err, "error creating the IngressPodControllers")
	}

	endpointsControllers, err := NewServiceExportEndpointsControllers(g.syncerConfig)
	if err != nil {
		return errors.Wrap(err, "error creating the Endpoints controller")
	}

	c, err = NewServiceExportController(g.syncerConfig, podControllers, endpointsControllers)
	if err != nil {
		return errors.Wrap(err, "error creating the ServiceExport controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewServiceController(g.syncerConfig, podControllers)
	if err != nil {
		return errors.Wrap(err, "error creating the Service controller")
	}

	g.controllers = append(g.controllers, c)

	for _, c := range g.controllers {
		err = c.Start()
		if err != nil {
			return err // nolint:wrapcheck  // Let the caller wrap it
		}
	}

	klog.Infof("Successfully started the controllers")

	return nil
}

func (g *gatewayMonitor) stopControllers() {
	for _, c := range g.controllers {
		c.Stop()
	}

	g.controllers = nil
}
