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
	"sync/atomic"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/watcher"
	iptiface "github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/ipset"
	"github.com/submariner-io/submariner/pkg/iptables"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// Globalnet uses MARK target to mark traffic destined to remote clusters.
	// Some of the CNIs also use iptable MARK targets in the pipeline. This should not
	// be a problem because Globalnet is only marking traffic destined to Submariner
	// connected clusters where Submariner takes full control on how the traffic is
	// steered in the pipeline. Normal traffic should not be affected because of this.
	globalNetIPTableMark = "0xC0000/0xC0000"

	// This is an internal annotation used between ingress pod controller and global-ingress controller.
	headlessSvcPodIP = "submariner.io/headless-svc-pod-ip"

	// This is an internal annotation used between ingress endpoints controller and global-ingress controller.
	headlessSvcEndpointsIP = "submariner.io/headless-svc-endpoints-ip"

	ServiceRefLabel = "submariner.io/serviceRef"

	// InternalServicePrefix is a prefix used for internal services.
	InternalServicePrefix = "submariner-"

	// InternalServiceLabel is a label applied on the internal service created by Globalnet controller and
	// it points to the exported service.
	InternalServiceLabel = "submariner.io/exportedServiceRef"

	// InternalServiceFinalizer is applied on the internal services created by Globalnet controller
	// to protect them from accidental deletion.
	InternalServiceFinalizer = "submariner.io/globalnet-internal-service"

	// The prefix used for the ipset chains created by Globalnet pod.
	IPSetPrefix = "SM-GN-"

	AddRules    = true
	DeleteRules = false

	DefaultNumberOfClusterEgressIPs = 8
)

type Interface interface {
	Start() error
	Stop()
}

type Specification struct {
	ClusterID   string
	Namespace   string
	GlobalCIDR  []string
	MetricsPort string `default:"32781"`
	Uninstall   bool
}

type baseController struct {
	stopCh chan struct{}
}

type gatewayMonitor struct {
	*baseController
	syncerConfig     *syncer.ResourceSyncerConfig
	endpointWatcher  watcher.Interface
	spec             Specification
	ipt              iptables.Interface
	isGatewayNode    atomic.Bool
	nodeName         string
	localSubnets     []string
	remoteSubnets    sets.Set[string]
	controllersMutex sync.Mutex // Protects controllers
	controllers      []Interface
}

type baseSyncerController struct {
	*baseController
	resourceSyncer syncer.Interface
}

type baseIPAllocationController struct {
	*baseSyncerController
	pool     *ipam.IPPool
	iptIface iptiface.Interface
}

type globalEgressIPController struct {
	*baseIPAllocationController
	sync.Mutex
	podWatchers   map[string]*egressPodWatcher
	ipSetIface    ipset.Interface
	watcherConfig watcher.Config
}

type egressPodWatcher struct {
	stopCh      chan struct{}
	ipSetName   string
	namedIPSet  ipset.Named
	podSelector *metav1.LabelSelector
}

type clusterGlobalEgressIPController struct {
	*baseIPAllocationController
	localSubnets []string
}

type globalIngressIPController struct {
	*baseIPAllocationController
	services dynamic.NamespaceableResourceInterface
	scheme   *runtime.Scheme
}

type serviceExportController struct {
	*baseSyncerController
	services                    dynamic.NamespaceableResourceInterface
	ingressIPs                  dynamic.ResourceInterface
	iptIface                    iptiface.Interface
	podControllers              *IngressPodControllers
	endpointsControllers        *ServiceExportEndpointsControllers
	ingressEndpointsControllers *IngressEndpointsControllers
	scheme                      *runtime.Scheme
}

type serviceController struct {
	*baseSyncerController
	ingressIPs     dynamic.ResourceInterface
	podControllers *IngressPodControllers
}

type nodeController struct {
	*baseIPAllocationController
	nodeName string
	nodes    dynamic.ResourceInterface
}

type ingressPodController struct {
	*baseSyncerController
	svcName      string
	namespace    string
	ingressIPMap sets.Set[string]
}

type IngressPodControllers struct {
	mutex       sync.Mutex
	controllers map[string]*ingressPodController
	config      syncer.ResourceSyncerConfig
	ingressIPs  dynamic.NamespaceableResourceInterface
}

type endpointsController struct {
	*baseSyncerController
	name      string
	namespace string
	endpoints dynamic.NamespaceableResourceInterface
}

type ServiceExportEndpointsControllers struct {
	mutex       sync.Mutex
	controllers map[string]*endpointsController
	config      syncer.ResourceSyncerConfig
}

type ingressEndpointsController struct {
	*baseSyncerController
	svcName    string
	namespace  string
	config     syncer.ResourceSyncerConfig
	ingressIPs dynamic.NamespaceableResourceInterface
}

type IngressEndpointsControllers struct {
	mutex       sync.Mutex
	controllers map[string]*ingressEndpointsController
	config      syncer.ResourceSyncerConfig
	ingressIPs  dynamic.NamespaceableResourceInterface
}

var logger = log.Logger{Logger: logf.Log.WithName("Globalnet")}
