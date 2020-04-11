package ipam

import (
	"sync"

	"github.com/coreos/go-iptables/iptables"
	kubeInformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
)

type SubmarinerIpamControllerSpecification struct {
	ClusterID string
	ExcludeNS []string
	Namespace string
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

	excludeNamespaces map[string]bool
	pool              *IpPool

	ipt *iptables.IPTables
}

type GatewayMonitor struct {
	clusterID           string
	kubeClientSet       kubernetes.Interface
	submarinerClientSet clientset.Interface
	endpointWorkqueue   workqueue.RateLimitingInterface
	endpointsSynced     cache.InformerSynced
	ipamSpec            *SubmarinerIpamControllerSpecification
	ipt                 *iptables.IPTables
	stopProcessing      chan struct{}
	isGatewayNode       bool
	syncMutex           *sync.Mutex
}
