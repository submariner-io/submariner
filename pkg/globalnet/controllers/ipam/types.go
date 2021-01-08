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

	"github.com/submariner-io/submariner/pkg/iptables"
	kubeInformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
)

type SubmarinerIpamControllerSpecification struct {
	ClusterID  string
	ExcludeNS  []string
	Namespace  string
	GlobalCIDR []string
}

type InformerConfigStruct struct {
	KubeClientSet   kubernetes.Interface
	ServiceInformer kubeInformers.ServiceInformer
	PodInformer     kubeInformers.PodInformer
	NodeInformer    kubeInformers.NodeInformer
}

type Controller struct {
	kubeClientSet    kubernetes.Interface
	serviceWorkqueue workqueue.RateLimitingInterface
	servicesSynced   cache.InformerSynced
	podWorkqueue     workqueue.RateLimitingInterface
	podsSynced       cache.InformerSynced
	nodeWorkqueue    workqueue.RateLimitingInterface
	nodesSynced      cache.InformerSynced
	gwNodeName       string

	excludeNamespaces map[string]bool
	pool              *IpPool

	ipt iptables.Interface
}

type GatewayMonitor struct {
	clusterID           string
	kubeClientSet       kubernetes.Interface
	submarinerClientSet clientset.Interface
	endpointWorkqueue   workqueue.RateLimitingInterface
	endpointsSynced     cache.InformerSynced
	ipamSpec            *SubmarinerIpamControllerSpecification
	ipt                 iptables.Interface
	stopProcessing      chan struct{}
	isGatewayNode       bool
	nodeName            string
	syncMutex           *sync.Mutex
}
