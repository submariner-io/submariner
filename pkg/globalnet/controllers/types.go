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
	"sync"

	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/watcher"
	iptiface "github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/iptables"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
)

const (
	ClusterGlobalEgressIPName = "cluster-egress.submariner.io"

	// Globalnet uses MARK target to mark traffic destined to remote clusters.
	// Some of the CNIs also use iptable MARK targets in the pipeline. This should not
	// be a problem because Globalnet is only marking traffic destined to Submariner
	// connected clusters where Submariner takes full control on how the traffic is
	// steered in the pipeline. Normal traffic should not be affected because of this.
	globalNetIPTableMark = "0xC0000/0xC0000"

	// This is an internal annotation used between service export controller and global-ingress controller.
	kubeProxyIPTableChainAnnotation = "submariner.io/kubeproxy-iptablechain"

	// Currently Submariner Globalnet implementation (for services) works with kube-proxy
	// and uses iptable chain-names programmed by kube-proxy. If the internal implementation
	// of kube-proxy changes, globalnet needs to be modified accordingly.
	// Reference: https://bit.ly/2OPhlwk
	kubeProxyServiceChainPrefix = "KUBE-SVC-"

	AddRules    = true
	DeleteRules = false
)

type Interface interface {
	Start() error
	Stop()
}

type Specification struct {
	ClusterID  string
	Namespace  string
	GlobalCIDR []string
}

type baseController struct {
	stopCh chan struct{}
}

type gatewayMonitor struct {
	*baseController
	syncerConfig    *syncer.ResourceSyncerConfig
	endpointWatcher watcher.Interface
	spec            Specification
	ipt             iptables.Interface
	isGatewayNode   bool
	nodeName        string
	syncMutex       sync.Mutex
	localSubnets    []string
	remoteSubnets   stringset.Interface
	controllers     []Interface
}

type baseSyncerController struct {
	*baseController
	resourceSyncer syncer.Interface
}

type baseIPAllocationController struct {
	*baseSyncerController
	pool *ipam.IPPool
}

type globalEgressIPController struct {
	*baseIPAllocationController
	sync.Mutex
	podWatchers   map[string]*podWatcher
	watcherConfig watcher.Config
}

type podWatcher struct {
	stopCh chan struct{}
}

type clusterGlobalEgressIPController struct {
	*baseIPAllocationController
	iptIface     iptiface.Interface
	localSubnets []string
}

type globalIngressIPController struct {
	*baseIPAllocationController
	iptIface iptiface.Interface
}

type serviceExportController struct {
	*baseSyncerController
	services dynamic.NamespaceableResourceInterface
	scheme   *runtime.Scheme
	iptIface iptiface.Interface
}
