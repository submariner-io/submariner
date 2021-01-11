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
package cleanup

import (
	"fmt"

	"github.com/coreos/go-iptables/iptables"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"

	"k8s.io/klog"
)

const (
	GN_Status_Not_Verified = iota
	GN_Enabled
	GN_Disabled
)

type GlobalnetStatus int

func GetGlobalnetCleanupHandlers(clusterID, objectNamespace string, submarinerClientSet clientset.Interface) []cleanup.Handler {
	return []cleanup.Handler{
		newCleanupGlobalnetRules(clusterID, objectNamespace, submarinerClientSet),
	}
}

type cleanupGlobalnetRules struct {
	clusterID       string
	objectNamespace string
	clientSet       clientset.Interface
	globalnetStatus GlobalnetStatus
}

func newCleanupGlobalnetRules(clusterID, objectNamespace string, submarinerClientSet clientset.Interface) cleanup.Handler {
	return &cleanupGlobalnetRules{
		clusterID:       clusterID,
		objectNamespace: objectNamespace,
		clientSet:       submarinerClientSet,
		globalnetStatus: GN_Status_Not_Verified,
	}
}

func (gn *cleanupGlobalnetRules) GetName() string {
	return "Globalnet rules cleanup handler"
}

func (gn *cleanupGlobalnetRules) NonGatewayCleanup() error {
	return nil
}

func (gn *cleanupGlobalnetRules) GatewayToNonGatewayTransition() error {
	if gn.globalnetStatus == GN_Status_Not_Verified {
		localCluster, err := gn.clientSet.SubmarinerV1().Clusters(gn.objectNamespace).Get(gn.clusterID, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("error while retrieving the local ClusterInfo: %v", err)
		}

		if len(localCluster.Spec.GlobalCIDR) > 0 {
			gn.globalnetStatus = GN_Enabled
		} else {
			gn.globalnetStatus = GN_Disabled
		}
	}

	if gn.globalnetStatus == GN_Enabled {
		ClearGlobalnetChains()
	}

	return nil
}

func ClearGlobalnetChains() {
	klog.Info("Globalnet is enabled and active gateway migrated, flushing Globalnet chains.")

	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("error initializing iptables: %v", err)
		return
	}

	if err = ipt.ClearChain("nat", constants.SmGlobalnetIngressChain); err != nil {
		klog.Errorf("Error while flushing rules in %s chain: %v", constants.SmGlobalnetIngressChain, err)
	}

	if err = ipt.ClearChain("nat", constants.SmGlobalnetEgressChain); err != nil {
		klog.Errorf("Error while flushing rules in %s chain: %v", constants.SmGlobalnetEgressChain, err)
	}

	if err = ipt.ClearChain("nat", constants.SmGlobalnetMarkChain); err != nil {
		klog.Errorf("Error while flushing rules in %s chain: %v", constants.SmGlobalnetMarkChain, err)
	}
}
