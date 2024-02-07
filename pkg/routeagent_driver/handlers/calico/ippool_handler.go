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

package calico

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	calicoapi "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	calicocs "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/util"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	errorutils "k8s.io/apimachinery/pkg/util/errors"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	SubmarinerIPPool       = "submariner.io/ippool"
	GwLBSvcName            = "submariner-gateway"
	GwLBSvcROKSAnnotation  = "service.kubernetes.io/ibm-load-balancer-cloud-provider-ip-type"
	DefaultV4IPPoolName    = "default-ipv4-ippool"
	submarinerManagedLabel = "submariner-managed"
	submarinerPrevIPIPMode = "submariner-prev-ipipmode"
)

type calicoIPPoolHandler struct {
	event.HandlerBase
	restConfig *rest.Config
	client     calicocs.Interface
	k8sClient  clientset.Interface
	namespace  string
}

var NewClient = func(restConfig *rest.Config) (calicocs.Interface, error) {
	return calicocs.NewForConfig(restConfig) //nolint:wrapcheck // No need to wrap
}

var logger = log.Logger{Logger: logf.Log.WithName("CalicoIPPool")}

func NewCalicoIPPoolHandler(restConfig *rest.Config, namespace string, k8sClient clientset.Interface) event.Handler {
	return &calicoIPPoolHandler{
		restConfig: restConfig,
		namespace:  namespace,
		k8sClient:  k8sClient,
	}
}

func (h *calicoIPPoolHandler) GetNetworkPlugins() []string {
	return []string{cni.Calico}
}

func (h *calicoIPPoolHandler) GetName() string {
	return "Calico IPPool handler"
}

func (h *calicoIPPoolHandler) Init() error {
	var err error

	if h.client, err = NewClient(h.restConfig); err != nil {
		return errors.Wrap(err, "error initializing Calico clientset")
	}

	return h.updateROKSCalicoCfg()
}

func (h *calicoIPPoolHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	if !h.State().IsOnGateway() {
		logger.V(log.TRACE).Info("Ignore RemoteEndpointCreated event (node isn't Gateway)")
		return nil
	}

	err := h.createIPPool(endpoint)

	return errors.Wrap(err, "failed to handle RemoteEndpointCreated event")
}

func (h *calicoIPPoolHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	if !h.State().IsOnGateway() {
		logger.V(log.TRACE).Info("Ignore RemoteEndpointRemoved event (node isn't Gateway)")
		return nil
	}

	err := h.deleteIPPool(endpoint)

	return errors.Wrap(err, "failed to handle RemoteEndpointRemoved event")
}

func (h *calicoIPPoolHandler) TransitionToGateway() error {
	var retErrors []error
	logger.Info("TransitionToGateway")

	endpoints := h.State().GetRemoteEndpoints()
	for i := range endpoints {
		err := h.createIPPool(&endpoints[i])
		if err != nil {
			logger.Warningf("Failed to create ippool %s", endpoints[i].GetName())
			retErrors = append(retErrors,
				errors.Wrapf(err, "error creating Calico IPPool for endpoint %q ", endpoints[i].GetName()))
		}
	}

	return errorutils.NewAggregate(retErrors)
}

func (h *calicoIPPoolHandler) Uninstall() error {
	logger.Info("Uninstalling Calico IPPools used for Submariner")

	labelSelector := labels.SelectorFromSet(map[string]string{SubmarinerIPPool: "true"}).String()
	err := h.client.ProjectcalicoV3().IPPools().DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: labelSelector})

	if err != nil && !apierrors.IsNotFound(err) {
		return errors.Wrapf(err, "Failed to delete Calico IPPools using labelSelector %q", labelSelector)
	}

	logger.Infof("Successfully delete Calico IPPools using labelSelector %q", labelSelector)

	if err := h.restoreROKSCalicoCfg(); err != nil {
		return err
	}

	return nil
}

func (h *calicoIPPoolHandler) createIPPool(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractIPv4Subnets(endpoint.Spec.Subnets)
	var retErrors []error

	for _, subnet := range subnets {
		iPPoolObj := &calicoapi.IPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:   getEndpointSubnetIPPoolName(endpoint, subnet),
				Labels: map[string]string{SubmarinerIPPool: "true"},
			},
			Spec: calicoapi.IPPoolSpec{
				CIDR:        subnet,
				NATOutgoing: false,
				Disabled:    true,
			},
		}
		_, err := h.client.ProjectcalicoV3().IPPools().Create(context.TODO(), iPPoolObj, metav1.CreateOptions{})

		if err == nil {
			logger.Infof("Successfully created Calico IPPool %q", iPPoolObj.GetName())
			continue
		}

		if !apierrors.IsAlreadyExists(err) {
			retErrors = append(retErrors,
				errors.Wrapf(err, "error creating Calico IPPool for ClusterID %q subnet %q (is Calico API server running?)",
					endpoint.Spec.ClusterID, subnet))
		}
	}

	return errorutils.NewAggregate(retErrors)
}

func (h *calicoIPPoolHandler) deleteIPPool(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractIPv4Subnets(endpoint.Spec.Subnets)
	var retErrors []error

	for _, subnet := range subnets {
		poolName := getEndpointSubnetIPPoolName(endpoint, subnet)

		err := h.client.ProjectcalicoV3().IPPools().Delete(context.TODO(),
			poolName, metav1.DeleteOptions{})

		if err == nil {
			logger.Infof("Successfully deleted Calico IPPool %q", poolName)
			continue
		}

		if !apierrors.IsNotFound(err) {
			retErrors = append(retErrors,
				errors.Wrapf(err, "error deleting Calico IPPool for ClusterID %q subnet %q (is Calico API server running?)",
					endpoint.Spec.ClusterID, subnet))
		}
	}

	return errorutils.NewAggregate(retErrors)
}

func getEndpointSubnetIPPoolName(endpoint *submV1.Endpoint, subnet string) string {
	return fmt.Sprintf("submariner-%s-%s", endpoint.Spec.ClusterID, strings.ReplaceAll(subnet, "/", "-"))
}

func (h *calicoIPPoolHandler) platformIsROKS() (bool, error) {
	// Submariner GW is deployed on ROKS using LB service with specific annotations.
	service, err := h.k8sClient.CoreV1().Services(h.namespace).Get(context.TODO(), GwLBSvcName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, errors.Wrap(err, "error reading gw lb service")
	}

	return service.GetAnnotations()[GwLBSvcROKSAnnotation] != "", nil
}

// workaround to address datapath issue with default Calico IPPool configuration for ROKS platform,
// IPIPMode of calico default IPPool should be set to 'Always'.
func (h *calicoIPPoolHandler) updateROKSCalicoCfg() error {
	isROKS, err := h.platformIsROKS()
	if err != nil {
		return err
	}

	if !isROKS {
		return nil
	}

	// platform is ROKS, make sure that IPIPMode of default IPPool is Always

	err = util.Update(context.TODO(), h.iPPoolResourceInterface(), &calicoapi.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultV4IPPoolName,
		},
	}, func(existing *calicoapi.IPPool) (*calicoapi.IPPool, error) {
		if existing.Spec.IPIPMode == calicoapi.IPIPModeAlways {
			logger.Infof("IPIPMode of %s IPPool already set to Always", DefaultV4IPPoolName)
			return existing, nil // no need to update
		}

		if existing.Labels == nil {
			existing.Labels = make(map[string]string)
		}

		if existing.Annotations == nil {
			existing.Annotations = make(map[string]string)
		}

		// mark 'submariner-managed' label for persistency
		existing.Labels[submarinerManagedLabel] = "true"
		existing.Annotations[submarinerPrevIPIPMode] = string(existing.Spec.IPIPMode)
		existing.Spec.IPIPMode = calicoapi.IPIPModeAlways
		logger.Infof("Setting IPIPMode of %s IPPool to Always", DefaultV4IPPoolName)
		return existing, nil
	})

	return errors.Wrap(err, "error updating default ippool")
}

func (h *calicoIPPoolHandler) restoreROKSCalicoCfg() error {
	err := util.Update(context.TODO(), h.iPPoolResourceInterface(), &calicoapi.IPPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: DefaultV4IPPoolName,
		},
	}, func(existing *calicoapi.IPPool) (*calicoapi.IPPool, error) {
		prevIPIPModeCfg := existing.Annotations[submarinerPrevIPIPMode]
		if prevIPIPModeCfg == "" {
			return existing, nil // no need to update
		}
		existing.Spec.IPIPMode = calicoapi.IPIPMode(prevIPIPModeCfg)
		delete(existing.Labels, submarinerManagedLabel)
		delete(existing.Annotations, submarinerPrevIPIPMode)

		return existing, nil
	})

	return errors.Wrap(err, "error updating default ippool")
}

func (h *calicoIPPoolHandler) iPPoolResourceInterface() resource.Interface[*calicoapi.IPPool] {
	return &resource.InterfaceFuncs[*calicoapi.IPPool]{
		GetFunc:    h.client.ProjectcalicoV3().IPPools().Get,
		CreateFunc: h.client.ProjectcalicoV3().IPPools().Create,
		UpdateFunc: h.client.ProjectcalicoV3().IPPools().Update,
		DeleteFunc: h.client.ProjectcalicoV3().IPPools().Delete,
	}
}
