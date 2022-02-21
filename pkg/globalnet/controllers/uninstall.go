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
	"context"
	"fmt"
	"strings"

	"github.com/submariner-io/admiral/pkg/finalizer"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	versioned "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipset"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	rest "k8s.io/client-go/rest"
	"k8s.io/klog"
	utilexec "k8s.io/utils/exec"
)

func UninstallDataPath() {
	ipt, err := iptables.New()
	if err != nil {
		klog.Fatal(err)
	}

	natTableChains := []string{
		// The chains have to be deleted in a specific order.
		constants.SmGlobalnetEgressChainForCluster,
		constants.SmGlobalnetEgressChainForHeadlessSvcPods,
		constants.SmGlobalnetEgressChainForNamespace,
		constants.SmGlobalnetEgressChainForPods,
		constants.SmGlobalnetIngressChain,
		constants.SmGlobalnetMarkChain,
		constants.SmGlobalnetEgressChain,
	}

	for _, chain := range natTableChains {
		err = ipt.FlushIPTableChain(constants.NATTable, chain)
		if err != nil {
			// Just log an error as this is part of uninstallation.
			klog.Error(err)
		}
	}

	err = ipt.FlushIPTableChain(constants.NATTable, routeAgent.SmPostRoutingChain)
	if err != nil {
		klog.Error(err)
	}

	if err := ipt.DeleteIPTableRule(constants.NATTable, "PREROUTING", constants.SmGlobalnetIngressChain); err != nil {
		klog.Errorf("error deleting iptables rule for %q in PREROUTING chain: %v\n", constants.SmGlobalnetIngressChain, err)
	}

	for _, chain := range natTableChains {
		err = ipt.DeleteIPTableChain(constants.NATTable, chain)
		if err != nil {
			klog.Error(err)
		}
	}

	ipsetIface := ipset.New(utilexec.New())

	ipSetList, err := ipsetIface.ListSets()
	if err != nil {
		klog.Errorf("Error listing ipsets: %v", err)
	}

	for _, set := range ipSetList {
		if strings.HasPrefix(set, IPSetPrefix) {
			err = ipsetIface.DestroySet(set)
			if err != nil {
				klog.Errorf("Error destroying the ipset %q: %v", set, err)
			}
		}
	}
}

func DeleteGlobalnetObjects(smClientSet *versioned.Clientset, cfg *rest.Config) {
	err := smClientSet.SubmarinerV1().ClusterGlobalEgressIPs("").DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Error deleting the clusterGlobalEgressIPs: %v", err)
	}

	err = smClientSet.SubmarinerV1().GlobalEgressIPs("").DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Error deleting the globalEgressIPs: %v", err)
	}

	deleteGlobalIngressIPs(smClientSet, cfg)
}

func deleteGlobalIngressIPs(smClientSet *versioned.Clientset, cfg *rest.Config) {
	defer deleteAllGlobalIngressIPObjs(smClientSet)

	restMapper, err := util.BuildRestMapper(cfg)
	if err != nil {
		klog.Errorf("Error creating the RestMapper: %v", err)
		return
	}

	_, gvr, err := util.ToUnstructuredResource(&corev1.Service{}, restMapper)
	if err != nil {
		klog.Errorf("Error converting resource: %v", err)
		return
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		klog.Errorf("Error creating dynamic client: %v", err)
		return
	}

	giipList, err := smClientSet.SubmarinerV1().GlobalIngressIPs("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Error listing the globalIngressIP objects: %v", err)
	}

	for _, giip := range giipList.Items {
		klog.Infof("Deleting the globalnet internal service: %q", giip.Name)

		err = deleteInternalService(giip, dynClient.Resource(*gvr))
		if err != nil {
			klog.Errorf("Error deleting the internal service: %v", err)
		}
	}
}

func deleteInternalService(ingressIP submarinerv1.GlobalIngressIP, services dynamic.NamespaceableResourceInterface) error {
	serviceRef := ingressIP.Spec.ServiceRef
	internalSvc := GetInternalSvcName(serviceRef.Name)
	key := fmt.Sprintf("%s/%s", ingressIP.Namespace, internalSvc)

	service, exists, _ := getService(internalSvc, ingressIP.Namespace, services, scheme.Scheme)
	if exists {
		if err := finalizer.Remove(context.TODO(), resource.ForDynamic(services.Namespace(ingressIP.Namespace)), service,
			InternalServiceFinalizer); err != nil {
			return fmt.Errorf("error while removing the finalizer from globalnet internal service %q", key)
		}

		_ = deleteService(ingressIP.Namespace, internalSvc, services)
	}

	return nil
}

func deleteAllGlobalIngressIPObjs(smClientSet *versioned.Clientset) {
	err := smClientSet.SubmarinerV1().GlobalIngressIPs("").DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Error deleting the globalIngressIPs: %v", err)
	}
}