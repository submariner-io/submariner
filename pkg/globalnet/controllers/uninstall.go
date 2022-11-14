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
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/finalizer"
	"github.com/submariner-io/admiral/pkg/resource"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	versioned "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipset"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	rest "k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	utilexec "k8s.io/utils/exec"
)

func UninstallDataPath() {
	ipt, err := iptables.New()
	logger.FatalOnError(err, "Error initializing IP tables")

	natTableChains := []string{
		// The chains have to be deleted in a specific order.
		constants.SmGlobalnetEgressChainForCluster,
		constants.SmGlobalnetEgressChainForHeadlessSvcPods,
		constants.SmGlobalnetEgressChainForHeadlessSvcEPs,
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
			logger.Errorf(err, "Error flushing iptables chain %q", chain)
		}
	}

	err = ipt.FlushIPTableChain(constants.NATTable, routeAgent.SmPostRoutingChain)
	if err != nil {
		logger.Errorf(err, "Error flushing iptables chain %q", routeAgent.SmPostRoutingChain)
	}

	if err := ipt.DeleteIPTableRule(constants.NATTable, "PREROUTING", constants.SmGlobalnetIngressChain); err != nil {
		logger.Errorf(err, "Error deleting iptables rule for %q in PREROUTING chain", constants.SmGlobalnetIngressChain)
	}

	for _, chain := range natTableChains {
		err = ipt.DeleteIPTableChain(constants.NATTable, chain)
		if err != nil {
			logger.Errorf(err, "Error deleting iptables chain %q", chain)
		}
	}

	ipsetIface := ipset.New(utilexec.New())

	ipSetList, err := ipsetIface.ListSets()
	if err != nil {
		logger.Errorf(err, "Error listing ipsets")
	}

	for _, set := range ipSetList {
		if strings.HasPrefix(set, IPSetPrefix) {
			err = ipsetIface.DestroySet(set)
			if err != nil {
				logger.Errorf(err, "Error destroying the ipset %q", set)
			}
		}
	}
}

func DeleteGlobalnetObjects(smClientSet *versioned.Clientset, cfg *rest.Config) {
	err := smClientSet.SubmarinerV1().ClusterGlobalEgressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Errorf(err, "Error deleting the clusterGlobalEgressIPs")
	}

	err = smClientSet.SubmarinerV1().GlobalEgressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Errorf(err, "Error deleting the globalEgressIPs")
	}

	deleteGlobalIngressIPs(smClientSet, cfg)
}

func deleteGlobalIngressIPs(smClientSet *versioned.Clientset, cfg *rest.Config) {
	defer deleteAllGlobalIngressIPObjs(smClientSet)

	gvr := schema.GroupVersionResource{
		Group:    corev1.SchemeGroupVersion.Group,
		Version:  corev1.SchemeGroupVersion.Version,
		Resource: "services",
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		logger.Errorf(err, "Error creating dynamic client")
		return
	}

	giipList, err := smClientSet.SubmarinerV1().GlobalIngressIPs(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		logger.Errorf(err, "Error listing the globalIngressIP objects")
	}

	services := dynClient.Resource(gvr)

	for i := range giipList.Items {
		logger.Infof("Deleting the globalnet internal service: %q", giipList.Items[i].Name)

		err = deleteInternalService(&giipList.Items[i], services)
		if err != nil && !apierrors.IsNotFound(err) {
			logger.Errorf(err, "Error deleting the internal service")
		}
	}
}

func deleteInternalService(ingressIP *submarinerv1.GlobalIngressIP, services dynamic.NamespaceableResourceInterface) error {
	serviceRef := ingressIP.Spec.ServiceRef
	internalSvc := GetInternalSvcName(serviceRef.Name)
	key := fmt.Sprintf("%s/%s", ingressIP.Namespace, internalSvc)

	service, exists, _ := getService(internalSvc, ingressIP.Namespace, services, scheme.Scheme)
	if exists {
		if err := finalizer.Remove(context.TODO(), resource.ForDynamic(services.Namespace(ingressIP.Namespace)), service,
			InternalServiceFinalizer); err != nil {
			return fmt.Errorf("error while removing the finalizer from globalnet internal service %q", key)
		}

		err := deleteService(ingressIP.Namespace, internalSvc, services)
		if err != nil && !apierrors.IsNotFound(err) {
			logger.Errorf(err, "Error deleting the service %q/%q", ingressIP.Namespace, internalSvc)
		}
	}

	return nil
}

func deleteAllGlobalIngressIPObjs(smClientSet *versioned.Clientset) {
	err := smClientSet.SubmarinerV1().GlobalIngressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Errorf(err, "Error deleting the globalIngressIPs")
	}
}

func RemoveGlobalIPAnnotationOnNode(cfg *rest.Config) {
	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		logger.Errorf(nil, "Error reading the NODE_NAME from the environment")
		return
	}

	k8sClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Fatalf("Error building clientset: %s", err.Error())
	}

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := k8sClientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		if err != nil {
			return errors.Wrapf(err, "unable to get node info for node %q", nodeName)
		}

		annotations := node.GetAnnotations()
		if annotations == nil || annotations[constants.SmGlobalIP] == "" {
			return nil
		}

		delete(annotations, constants.SmGlobalIP)

		node.SetAnnotations(annotations)
		_, updateErr := k8sClientSet.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
		return updateErr //nolint:wrapcheck // We wrap it below in the enclosing function
	})

	if retryErr != nil {
		logger.Errorf(retryErr, "Error updating node %q", nodeName)
		return
	}

	logger.Infof("Successfully removed globalIP annotation from node %q", nodeName)
}
