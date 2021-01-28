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
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	kubeInformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
)

type SubmarinerIpamControllerSpecification struct {
	ClusterID  string
	ExcludeNS  []string
	Namespace  string
	GlobalCIDR []string
}

type InformerConfigStruct struct {
	KubeClientSet    kubernetes.Interface
	ServiceInformer  kubeInformers.ServiceInformer
	PodInformer      kubeInformers.PodInformer
	NodeInformer     kubeInformers.NodeInformer
	SvcExInformer    informers.GenericInformer
	DynamicClientSet dynamic.Interface
	SvcExGvr         schema.GroupVersionResource
}

type Controller struct {
	kubeClientSet    kubernetes.Interface
	dynClientSet     dynamic.Interface
	serviceWorkqueue workqueue.RateLimitingInterface
	servicesSynced   cache.InformerSynced
	podWorkqueue     workqueue.RateLimitingInterface
	podsSynced       cache.InformerSynced
	nodeWorkqueue    workqueue.RateLimitingInterface
	nodesSynced      cache.InformerSynced
	svcExWorkqueue   workqueue.RateLimitingInterface
	svcExSynced      bool
	svcExGvr         schema.GroupVersionResource
	gwNodeName       string

	excludeNamespaces map[string]bool
	pool              *IPPool

	ipt iptables.Interface
}

type GatewayMonitor struct {
	clusterID                 string
	kubeClientSet             kubernetes.Interface
	submarinerClientSet       clientset.Interface
	submarinerInformerFactory submarinerInformers.SharedInformerFactory
	endpointWorkqueue         workqueue.RateLimitingInterface
	endpointsSynced           cache.InformerSynced
	dynamicClientSet          dynamic.Interface
	ipamSpec                  *SubmarinerIpamControllerSpecification
	ipt                       iptables.Interface
	stopProcessing            chan struct{}
	isGatewayNode             bool
	nodeName                  string
	syncMutex                 sync.Mutex
}
