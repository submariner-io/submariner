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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/finalizer"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submclient "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	pfiface "github.com/submariner-io/submariner/pkg/globalnet/controllers/packetfilter"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
)

func UninstallDataPath() {
	gnpFilter, err := pfiface.New()
	logger.FatalOnError(err, "Error initializing GN PacketFilter")

	pFilter, err := packetfilter.New()
	logger.FatalOnError(err, "Error initializing PacketFilter")

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
		err = gnpFilter.FlushNatChain(chain)

		// Just log an error as this is part of uninstallation.
		logError(err, "Error flushing packetfilter chain %q", chain)
	}

	err = gnpFilter.FlushNatChain(routeAgent.SmPostRoutingChain)
	logError(err, "Error flushing packetfilter chain %q", routeAgent.SmPostRoutingChain)

	chain := packetfilter.ChainIPHook{
		Name:     constants.SmGlobalnetIngressChain,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPrerouting,
		Priority: packetfilter.ChainPriorityFirst,
	}

	err = pFilter.DeleteIPHookChain(&chain)
	logError(err, "error creating IPHook chain %s", constants.SmGlobalnetIngressChain)

	for _, chain := range natTableChains {
		if chain == constants.SmGlobalnetIngressChain {
			continue
		}

		err = gnpFilter.DeleteNatChain(chain)
		logError(err, "Error deleting iptables chain %q", chain)
	}

	err = pFilter.DestroySets(func(name string) bool {
		return strings.HasPrefix(name, IPSetPrefix)
	})
	logError(err, "Error destroying sets")
}

func DeleteGlobalnetObjects(smClientSet submclient.Interface, dynClient dynamic.Interface) {
	err := smClientSet.SubmarinerV1().ClusterGlobalEgressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	logError(err, "Error deleting the clusterGlobalEgressIPs")

	err = smClientSet.SubmarinerV1().GlobalEgressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	logError(err, "Error deleting the globalEgressIPs")

	deleteGlobalIngressIPs(smClientSet, dynClient)
}

func deleteGlobalIngressIPs(smClientSet submclient.Interface, dynClient dynamic.Interface) {
	defer deleteAllGlobalIngressIPObjs(smClientSet)

	gvr := schema.GroupVersionResource{
		Group:    corev1.SchemeGroupVersion.Group,
		Version:  corev1.SchemeGroupVersion.Version,
		Resource: "services",
	}

	giipList, err := smClientSet.SubmarinerV1().GlobalIngressIPs(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	logError(err, "Error listing the globalIngressIP objects")

	services := dynClient.Resource(gvr)

	for i := range giipList.Items {
		deleteInternalService(&giipList.Items[i], services)
	}
}

func deleteInternalService(ingressIP *submarinerv1.GlobalIngressIP, services dynamic.NamespaceableResourceInterface) {
	if ingressIP.Spec.ServiceRef == nil {
		return
	}

	internalSvc := GetInternalSvcName(ingressIP.Spec.ServiceRef.Name)
	key := fmt.Sprintf("%s/%s", ingressIP.Namespace, internalSvc)

	logger.Infof("Deleting the globalnet internal service: %q", key)

	service, exists, _ := getService(internalSvc, ingressIP.Namespace, services, scheme.Scheme)
	if exists {
		err := finalizer.Remove(context.TODO(), resource.ForDynamic(services.Namespace(ingressIP.Namespace)),
			resource.MustToUnstructured(service), InternalServiceFinalizer)
		logError(err, "rror while removing the finalizer from globalnet internal service %q", key)

		err = deleteService(ingressIP.Namespace, internalSvc, services)
		logError(err, "Error deleting the service %q/%q", ingressIP.Namespace, internalSvc)
	}
}

func deleteAllGlobalIngressIPObjs(smClientSet submclient.Interface) {
	err := smClientSet.SubmarinerV1().GlobalIngressIPs(metav1.NamespaceAll).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{})
	logError(err, "Error deleting the globalIngressIPs")
}

func RemoveStaleInternalServices(config *syncer.ResourceSyncerConfig) error {
	_, gvr, err := util.ToUnstructuredResource(&corev1.Service{}, config.RestMapper)
	if err != nil {
		return errors.Wrap(err, "error converting resource")
	}

	services := config.SourceClient.Resource(*gvr)

	svcList, err := services.Namespace(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{
		LabelSelector: InternalServiceLabel,
	})
	if err != nil {
		return errors.Wrap(err, "error listing the services")
	}

	for i := range svcList.Items {
		svc := &svcList.Items[i]
		if !svc.GetDeletionTimestamp().IsZero() {
			logger.Warningf("Globalnet internal service %s/%s has deletionTimestamp set", svc.GetNamespace(), svc.GetName())

			err := finalizer.Remove(context.TODO(), resource.ForDynamic(services.Namespace(svc.GetNamespace())),
				resource.MustToUnstructured(svc), InternalServiceFinalizer)
			if err != nil {
				return errors.Wrapf(err, "error while removing the finalizer from globalnet internal service %q/%q",
					svc.GetNamespace(), svc.GetName())
			}
		}
	}

	return nil
}

func logError(err error, format string, args ...interface{}) {
	if err != nil {
		logger.Errorf(err, format, args...)
	}
}
