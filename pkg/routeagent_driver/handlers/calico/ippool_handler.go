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
	"encoding/json"
	goerrors "errors"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	calicoapi "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	calicocs "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	"github.com/submariner-io/admiral/pkg/log"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	tigerav1 "github.com/tigera/operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	k8snet "k8s.io/utils/net"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	SubmarinerIPPool            = "submariner.io/ippool"
	GwLBSvcName                 = "submariner-gateway"
	GwLBSvcROKSAnnotation       = "service.kubernetes.io/ibm-load-balancer-cloud-provider-ip-type"
	DefaultV4IPPoolName         = "default-ipv4-ippool"
	submarinerManagedLabel      = "submariner-managed"
	submarinerPrevEncapsulation = "submariner-prev-encapsulation"
)

type calicoIPPoolHandler struct {
	event.HandlerBase
	restConfig *rest.Config
	client     calicocs.Interface
	dynClient  dynamic.Interface
	namespace  string
}

var NewClient = func(restConfig *rest.Config) (calicocs.Interface, error) {
	return calicocs.NewForConfig(restConfig)
}

var logger = log.Logger{Logger: logf.Log.WithName("CalicoIPPool")}

var gvrInstallations = schema.GroupVersionResource{
	Group:    "operator.tigera.io",
	Version:  "v1",
	Resource: "installations",
}

var gvrService = schema.GroupVersionResource{
	Group:    "", // Core API group
	Version:  "v1",
	Resource: "services",
}

func NewCalicoIPPoolHandler(restConfig *rest.Config, namespace string, dynClient dynamic.Interface) event.Handler {
	return &calicoIPPoolHandler{
		restConfig: restConfig,
		namespace:  namespace,
		dynClient:  dynClient,
	}
}

func (h *calicoIPPoolHandler) GetNetworkPlugins() []string {
	return []string{cni.Calico}
}

func (h *calicoIPPoolHandler) GetName() string {
	return "Calico IPPool handler"
}

func (h *calicoIPPoolHandler) Init(ctx context.Context) error {
	var err error

	if h.client, err = NewClient(h.restConfig); err != nil {
		return errors.Wrap(err, "error initializing Calico clientset")
	}

	return h.updateROKSCalicoCfg(ctx)
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

	return goerrors.Join(retErrors...)
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
	subnets := cidr.ExtractSubnets(k8snet.IPv4, endpoint.Spec.Subnets)
	var retErrors []error

	for _, subnet := range subnets {
		iPPoolObj := &calicoapi.IPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:   getEndpointSubnetIPPoolName(endpoint, subnet),
				Labels: map[string]string{SubmarinerIPPool: "true"},
			},
			Spec: calicoapi.IPPoolSpec{
				CIDR:             subnet,
				NATOutgoing:      false,
				Disabled:         true,
				DisableBGPExport: true,
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

	return goerrors.Join(retErrors...)
}

func (h *calicoIPPoolHandler) deleteIPPool(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractSubnets(k8snet.IPv4, endpoint.Spec.Subnets)
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

	return goerrors.Join(retErrors...)
}

func getEndpointSubnetIPPoolName(endpoint *submV1.Endpoint, subnet string) string {
	return fmt.Sprintf("submariner-%s-%s", endpoint.Spec.ClusterID, strings.ReplaceAll(subnet, "/", "-"))
}

func GetDefaultInstallation(dynamicClient dynamic.Interface, gvr schema.GroupVersionResource) (tigerav1.Installation, error) {
	installationUnstructured, err := dynamicClient.Resource(gvr).Get(context.TODO(), "default", metav1.GetOptions{})
	var installation tigerav1.Installation

	if err != nil {
		return installation, err
	}

	bytes, err := installationUnstructured.MarshalJSON()
	if err != nil {
		return installation, err
	}

	err = json.Unmarshal(bytes, &installation)
	if err != nil {
		return installation, err
	}

	return installation, nil
}

func PatchCalicoNetworkIpPoolsEncapsulation(dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, encapsulation string) error {
	patch := []byte(`[{"op": "replace", "path": "/spec/calicoNetwork/ipPools/0/encapsulation", "value": "` + encapsulation + `"}]`)
	_, err := dynamicClient.Resource(gvr).Patch(context.TODO(), "default", types.JSONPatchType, patch, metav1.PatchOptions{})

	return err
}

func (h *calicoIPPoolHandler) platformIsROKS(ctx context.Context) (bool, error) {
	// Submariner GW is deployed on ROKS using LB service with specific annotations.
	serviceUnstructured, err := h.dynClient.Resource(gvrService).Namespace(h.namespace).Get(ctx, GwLBSvcName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, errors.Wrap(err, "error reading gw lb service")
	}

	bytes, err := serviceUnstructured.MarshalJSON()
	if err != nil {
		return false, err
	}

	var service corev1.Service
	err = json.Unmarshal(bytes, &service)
	if err != nil {
		return false, err
	}

	return service.GetAnnotations()[GwLBSvcROKSAnnotation] != "", nil
}

// workaround to address datapath issue with default Calico IPPool configuration for ROKS platform,
// IPIPMode of calico default IPPool should be set to 'Always'.
func (h *calicoIPPoolHandler) updateROKSCalicoCfg(ctx context.Context) error {
	isROKS, err := h.platformIsROKS(ctx)
	if err != nil {
		return err
	}

	if !isROKS {
		return nil
	}

	// platform is ROKS, make sure that IPIPMode of default IPPool is Always
	/*
		err = util.Update(ctx, h.iPPoolResourceInterface(), &calicoapi.IPPool{
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
	*/

	installation, err := GetDefaultInstallation(h.dynClient, gvrInstallations)
	if err != nil {
		return err
	}

	encapsulation := installation.Spec.CalicoNetwork.IPPools[0].Encapsulation.String()

	patch := []byte(`[{"op": "add", "path": "/metadata/annotations", "value":{"` + submarinerPrevEncapsulation + `":"` + encapsulation + `"}}]`)
	_, err = h.dynClient.Resource(gvrInstallations).Patch(context.TODO(), "default", types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}

	err = PatchCalicoNetworkIpPoolsEncapsulation(h.dynClient, gvrInstallations, tigerav1.EncapsulationIPIP.String())
	if err != nil {
		return err
	}

	return nil
}

func (h *calicoIPPoolHandler) restoreROKSCalicoCfg() error {
	/*
		err := util.Update(context.TODO(), h.iPPoolResourceInterface(), &calicoapi.IPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name: DefaultV4IPPoolName,
			},
		}, func(existing *calicoapi.IPPool) (*calicoapi.IPPool, error) {
			prevEncapsulation := existing.Annotations[submarinerPrevEncapsulation]
			if prevEncapsulation == "" {
				return existing, nil // no need to update
			}

			existing.Spec.IPIPMode = calicoapi.IPIPMode(prevEncapsulation)
			delete(existing.Labels, submarinerManagedLabel)
			delete(existing.Annotations, submarinerPrevEncapsulation)

			return existing, nil
		})

		return errors.Wrap(err, "error updating default ippool")
	*/

	installation, err := GetDefaultInstallation(h.dynClient, gvrInstallations)
	if err != nil {
		return err
	}

	prevEncapsulation := installation.Annotations[submarinerPrevEncapsulation]
	if prevEncapsulation == "" {
		return nil // no need to update
	}

	patch := []byte(`[{"op": "replace", "path": "/spec/calicoNetwork/ipPools/0/encapsulation", "value": "` + prevEncapsulation + `"}]`)
	_, err = h.dynClient.Resource(gvrInstallations).Patch(context.TODO(), "default", types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}

	patch = []byte(`[{"op": "remove", "path": "/metadata/annotations/"` + submarinerPrevEncapsulation + `"}]`)
	_, err = h.dynClient.Resource(gvrInstallations).Patch(context.TODO(), "default", types.JSONPatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}

	return nil
}

/*
func (h *calicoIPPoolHandler) iPPoolResourceInterface() resource.Interface[*calicoapi.IPPool] {
	return &resource.InterfaceFuncs[*calicoapi.IPPool]{
		GetFunc:    h.client.ProjectcalicoV3().IPPools().Get,
		CreateFunc: h.client.ProjectcalicoV3().IPPools().Create,
		UpdateFunc: h.client.ProjectcalicoV3().IPPools().Update,
		DeleteFunc: h.client.ProjectcalicoV3().IPPools().Delete,
	}
}
*/
