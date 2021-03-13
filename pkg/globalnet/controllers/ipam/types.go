/*
© 2021 Red Hat, Inc. and others

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
package ipam

import (
	"sync"

	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/submariner/pkg/iptables"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
)

type SubmarinerIPAMControllerSpecification struct {
	ClusterID  string
	ExcludeNS  []string
	Namespace  string
	GlobalCIDR []string
}

type Controller struct {
	serviceLock         sync.Mutex
	serviceSyncer       syncer.Interface
	serviceExportSyncer syncer.Interface
	syncers             []syncer.Interface
	serviceClient       dynamic.NamespaceableResourceInterface
	scheme              *runtime.Scheme
	gwNodeName          string

	excludeNamespaces map[string]bool
	pool              *IPPool

	ipt iptables.Interface
}

type GatewayMonitor struct {
	clusterID       string
	syncerConfig    *syncer.ResourceSyncerConfig
	endpointWatcher watcher.Interface
	ipamSpec        *SubmarinerIPAMControllerSpecification
	ipt             iptables.Interface
	stopProcessing  chan struct{}
	isGatewayNode   bool
	nodeName        string
	syncMutex       sync.Mutex
}
