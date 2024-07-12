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

package ovn

import (
	"os"
	"sync/atomic"

	"github.com/pkg/errors"
	nodeutil "github.com/submariner-io/submariner/pkg/node"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type TransitSwitchIPGetter interface {
	Get() string
}

type TransitSwitchIP interface {
	TransitSwitchIPGetter
	Init(k8sClient kubernetes.Interface) error
	UpdateFrom(node *corev1.Node) (bool, error)
}

type transitSwitchIPImpl struct {
	value atomic.Value
}

func NewTransitSwitchIP() TransitSwitchIP {
	t := &transitSwitchIPImpl{}
	t.value.Store("")

	return t
}

func (t *transitSwitchIPImpl) Get() string {
	return t.value.Load().(string)
}

func (t *transitSwitchIPImpl) Init(k8sClient kubernetes.Interface) error {
	node, err := nodeutil.GetLocalNode(k8sClient)
	if err != nil {
		return errors.Wrap(err, "error getting the local node")
	}

	_, err = t.UpdateFrom(node)

	return err
}

func (t *transitSwitchIPImpl) UpdateFrom(node *corev1.Node) (bool, error) {
	if node.Name != os.Getenv("NODE_NAME") {
		return false, nil
	}

	value, ok := node.Annotations[constants.OvnTransitSwitchIPAnnotation]
	if !ok {
		logger.Infof("No transit switch IP configured on node %q", node.Name)
		return false, nil
	}

	transitSwitchIP, err := jsonToIP(value)
	if err != nil {
		return false, errors.Wrapf(err, "error parsing the transit switch IP")
	}

	return transitSwitchIP != t.value.Swap(transitSwitchIP), nil
}
