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

package node

import (
	"context"
	"os"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	nodeutil "k8s.io/component-helpers/node/util"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("Node")}

// These are public to allow unit tests to override.
var (
	PollTimeout  = time.Second * 30
	PollInterval = time.Second
)

func GetLocalNodeName() string {
	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		panic("Error reading the NODE_NAME from the environment")
	}

	return nodeName
}

func GetLocalNode(ctx context.Context, clientset kubernetes.Interface) (*v1.Node, error) {
	nodeName := GetLocalNodeName()

	var node *v1.Node

	err := wait.PollUntilContextTimeout(ctx, PollInterval, PollTimeout, true,
		func(ctx context.Context) (bool, error) {
			var err error

			node, err = clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
			if err == nil {
				return true, nil
			}

			logger.Warningf("Error retrieving the local node %q - retrying: %v", nodeName, err)

			return false, nil
		})

	return node, errors.Wrapf(err, "failed to get local node %q", nodeName)
}

func WaitForLocalNodeReady(ctx context.Context, client kubernetes.Interface) {
	// In most cases the node will already be ready; otherwise, wait forever or until the context is cancelled.
	err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		localNode, err := GetLocalNode(ctx, client)

		if err != nil {
			logger.Error(err, "Error retrieving local node")
		} else {
			_, condition := nodeutil.GetNodeCondition(&localNode.Status, v1.NodeReady)
			if condition != nil && condition.Status == v1.ConditionTrue {
				logger.Info("Local node ready")
				return true, nil
			}

			logger.Infof("Local node not ready - waiting. Conditions: %s", resource.ToJSON(localNode.Status.Conditions))
		}

		return false, nil
	})
	if err != nil {
		logger.Error(err, "Error waiting for local node")
	}
}
