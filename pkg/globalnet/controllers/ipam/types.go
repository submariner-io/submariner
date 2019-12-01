package ipam

import (
	"github.com/coreos/go-iptables/iptables"
	kubeInformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type SubmarinerIpamControllerSpecification struct {
	ClusterID  string
	GlobalCIDR string
	ExcludeNS  []string
}

type InformerConfigStruct struct {
	KubeClientSet   kubernetes.Interface
	ServiceInformer kubeInformers.ServiceInformer
	PodInformer     kubeInformers.PodInformer
}

type Controller struct {
	kubeClientSet    kubernetes.Interface
	serviceWorkqueue workqueue.RateLimitingInterface
	servicesSynced   cache.InformerSynced
	podWorkqueue     workqueue.RateLimitingInterface
	podsSynced       cache.InformerSynced

	excludeNamespaces map[string]bool
	pool              *IpPool

	ipt *iptables.IPTables
}
