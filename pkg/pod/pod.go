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

package pod

import (
	"context"
	"fmt"
	"os"

	"github.com/pkg/errors"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
)

type GatewayPodInterface interface {
	SetHALabels(status submV1.HAStatus) error
}

type GatewayPod struct {
	namespace string
	node      string
	name      string
	clientset kubernetes.Interface
}

func NewGatewayPod(k8sClient kubernetes.Interface) (*GatewayPod, error) {
	gp := &GatewayPod{
		namespace: os.Getenv("SUBMARINER_NAMESPACE"),
		node:      os.Getenv("NODE_NAME"),
		name:      os.Getenv("POD_NAME"),
		clientset: k8sClient,
	}

	if gp.namespace == "" {
		return nil, errors.New("SUBMARINER_NAMESPACE environment variable missing")
	}

	if gp.node == "" {
		return nil, errors.New("NODE_NAME environment variable missing")
	}

	if gp.name == "" {
		return nil, errors.New("POD_NAME environment variable missing")
	}

	if err := gp.SetHALabels(submV1.HAStatusPassive); err != nil {
		klog.Warningf("Error updating pod label: %s", err)
	}

	return gp, nil
}

const patchFormat = `{"metadata": {"labels": {"gateway.submariner.io/node": "%s", "gateway.submariner.io/status": "%s"}}}`

func (gp *GatewayPod) SetHALabels(status submV1.HAStatus) error {
	podsInterface := gp.clientset.CoreV1().Pods(gp.namespace)
	patch := fmt.Sprintf(patchFormat, gp.node, status)

	_, err := podsInterface.Patch(context.TODO(), gp.name, types.MergePatchType, []byte(patch), v1.PatchOptions{})
	if err != nil {
		return errors.Wrapf(err, "Error patching own pod %q in namespace %q with %s", gp.name, gp.namespace, patch)
	}

	return nil
}
