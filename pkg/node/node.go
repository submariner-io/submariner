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
	"encoding/json"
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

type AnnotationListModifier func(currentValue, newElement string, isAdd bool) (string, bool, error)

func GetLocalNode(ctx context.Context, clientset kubernetes.Interface) (*v1.Node, error) {
	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		return nil, errors.New("error reading the NODE_NAME from the environment")
	}

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

func ModifyLocalNodeAnnotationList(
	ctx context.Context,
	client kubernetes.Interface,
	annotationKey, newElement string,
	isAdd bool,
	modifierFunc AnnotationListModifier,
) error {
	node, err := GetLocalNode(ctx, client)
	if err != nil {
		return err
	}

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}

	currentValue := node.Annotations[annotationKey]
	newListValue, shouldUpdate, err := modifierFunc(currentValue, newElement, isAdd)

	if err != nil {
		return errors.Wrapf(err, "error processing modifierFunc for %v,%v", currentValue, newElement)
	}

	if !shouldUpdate {
		return nil
	}

	node.Annotations[annotationKey] = newListValue

	updateErr := wait.PollUntilContextTimeout(ctx, PollInterval, PollTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})

		if err != nil {
			logger.Warningf("Error updating node annotation %v — retrying: %v", annotationKey, err)
			return false, nil
		}

		return true, nil
	})

	if updateErr != nil {
		return errors.Wrapf(updateErr, "failed to update element %v in node annotation %v after retries", newElement, annotationKey)
	}

	if isAdd {
		logger.Infof("Added %v to node annotation[%v] successfully", newElement, annotationKey)
	} else {
		logger.Infof("Deleted %v from node annotation[%v] successfully", newElement, annotationKey)
	}

	return nil
}

func JSONListAnnotationModifier(currentListValue, newElement string, isAdd bool) (string, bool, error) {
	var list []string

	if currentListValue != "" {
		if err := json.Unmarshal([]byte(currentListValue), &list); err != nil {
			return "", false, errors.Wrap(err, "failed to unmarshal annotation list")
		}
	}

	exists := false

	for _, v := range list {
		if v == newElement {
			exists = true
			break
		}
	}

	if isAdd {
		if exists {
			return currentListValue, false, nil
		}

		list = append(list, newElement)
	} else {
		if !exists {
			return currentListValue, false, nil
		}

		newList := []string{}

		for _, v := range list {
			if v != newElement {
				newList = append(newList, v)
			}
		}

		list = newList
	}

	newBytes, err := json.Marshal(list)
	if err != nil {
		return "", false, errors.Wrap(err, "failed to marshal annotation list")
	}

	return string(newBytes), true, nil
}

func ClearLocalNodeAnnotation(
	ctx context.Context,
	client kubernetes.Interface,
	annotationKey string,
) error {
	node, err := GetLocalNode(ctx, client)
	if err != nil {
		return err
	}

	if node.Annotations == nil {
		return nil
	}

	if _, exists := node.Annotations[annotationKey]; !exists {
		return nil
	}

	delete(node.Annotations, annotationKey)

	updateErr := wait.PollUntilContextTimeout(ctx, PollInterval, PollTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
		if err != nil {
			logger.Warningf("Error clearing node annotation %v — retrying: %v", annotationKey, err)
			return false, nil
		}

		return true, nil
	})

	if updateErr != nil {
		return errors.Wrapf(updateErr, "failed to clear annotation %v after retries", annotationKey)
	}

	logger.Infof("Cleared node annotation[%v] successfully", annotationKey)

	return nil
}
