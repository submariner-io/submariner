package framework

import (
	"fmt"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
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
	f := &Framework{Framework: framework.NewFramework(baseName)}
	framework.AddCleanupAction(f.gatewayCleanup)
	return f
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

func (f *Framework) AwaitForGatewayWithStatus(cluster framework.ClusterIndex,
	name string, status submarinerv1.HAStatus) *submarinerv1.Gateway {
	gwClient := SubmarinerClients[cluster].SubmarinerV1().Gateways(framework.TestContext.SubmarinerNamespace)
	gw := framework.AwaitUntil("await gateway ready",
		func() (interface{}, error) {
			resGw, err := gwClient.Get(name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return resGw, err
		},
		func(result interface{}) (bool, string, error) {
			if result == nil {
				return false, "gateway not found yet", nil
			}
			gw := result.(*submarinerv1.Gateway)
			if gw.Status.HAStatus != status {
				return false, "", fmt.Errorf("Gateway %q showed up but has wrong status %q, expected %q",
					gw.Name, gw.Status.HAStatus, status)
			}
			return true, "", nil
		})
	return gw.(*submarinerv1.Gateway)
}

func (f *Framework) AwaitForGatewayGone(cluster framework.ClusterIndex, name string) {
	gwClient := SubmarinerClients[cluster].SubmarinerV1().Gateways(framework.TestContext.SubmarinerNamespace)
	framework.AwaitUntil("await gateway gone",
		func() (interface{}, error) {
			if _, err := gwClient.Get(name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, nil
		},
		func(result interface{}) (bool, string, error) {
			gone := result.(bool)
			return gone, "", nil
		})

}

// gatewayCleanup ensures that only the active gateway node is flagged as gateway node
//                which could not be after a failed test which left the system on an
//                unexpected state
func (f *Framework) gatewayCleanup() {
	gatewayClient := SubmarinerClients[framework.ClusterA].SubmarinerV1().Gateways(
		framework.TestContext.SubmarinerNamespace)
	gwList, err := gatewayClient.List(metav1.ListOptions{LabelSelector: submarinerv1.HAStatusPassiveSelector})
	Expect(err).NotTo(HaveOccurred())

	if len(gwList.Items) == 0 {
		return
	}

	ginkgo.By(fmt.Sprintf("Cleaning up any non-active gateways: %+v", gwList.Items))
	for _, nonActiveGw := range gwList.Items {
		f.SetGatewayLabelOnNode(framework.ClusterA, nonActiveGw.Name, false)
		f.AwaitForGatewayGone(framework.ClusterA, nonActiveGw.Name)
	}
}
