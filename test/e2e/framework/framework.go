package framework

import (
	"github.com/onsi/ginkgo"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace for you.
type Framework struct {
	*framework.Framework
}

var SubmarinerClients []*submarinerClientset.Clientset

func init() {
	framework.AddBeforeSuite(beforeSuite)
}

// NewFramework creates a test framework.
func NewFramework(baseName string) *Framework {
	return &Framework{Framework: framework.NewFramework(baseName)}
}

func beforeSuite() {
	ginkgo.By("Creating submariner clients")

	for _, restConfig := range framework.RestConfigs {
		SubmarinerClients = append(SubmarinerClients, createSubmarinerClient(restConfig))
	}

	queryAndUpdateGlobalnetStatus()
}

func queryAndUpdateGlobalnetStatus() {
	framework.TestContext.GlobalnetEnabled = false
	clusterIndex := framework.ClusterB
	clusterName := framework.TestContext.KubeContexts[clusterIndex]
	clusters := SubmarinerClients[clusterIndex].SubmarinerV1().Clusters(framework.TestContext.SubmarinerNamespace)
	framework.AwaitUntil("find the submariner Cluster for "+clusterName, func() (interface{}, error) {
		cluster, err := clusters.Get(clusterName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return cluster, err
	}, func(result interface{}) (bool, string, error) {
		if result == nil {
			return false, "No Cluster found", nil
		}

		cluster := result.(*submarinerv1.Cluster)
		if len(cluster.Spec.GlobalCIDR) != 0 {
			// Based on the status of GlobalnetEnabled, certain tests will be skipped/executed.
			framework.TestContext.GlobalnetEnabled = true
		}

		return true, "", nil
	})
}
