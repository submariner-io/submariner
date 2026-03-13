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

package clusterfiles

import (
	"context"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("ClusterFiles")}

// Get retrieves a config from a secret, configmap or file within the k8s cluster
// using an url schema that supports configmap://<namespace>/<configmap-name>/<data-file>
// secret://<namespace>/<secret-name>/<data-file> and file:///<path>.
func Get(ctx context.Context, k8sClient kubernetes.Interface, urlAddress string) ([]byte, error) {
	logger.V(log.DEBUG).Infof("Reading cluster_file: %s", urlAddress)

	parsedURL, err := url.Parse(urlAddress)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing cluster file URL %q", urlAddress)
	}

	namespace := parsedURL.Host
	pathContainerObject, pathFile := path.Split(parsedURL.Path)
	pathContainerObject = strings.Trim(pathContainerObject, "/")

	if pathContainerObject == "" || pathFile == "" {
		return nil, errors.Errorf("cluster file URL %q is not well formed", urlAddress)
	}

	switch parsedURL.Scheme {
	case "file":
		if parsedURL.Host != "" {
			return nil, errors.Errorf("cluster file %q is not well formed (file: URIs shouldn't specify a host)", urlAddress)
		}

		data, err := os.ReadFile(parsedURL.Path)
		if err != nil {
			return nil, errors.Wrapf(err, "error reading file %q", parsedURL.Path)
		}

		return data, nil

	case "secret":
		secret, err := k8sClient.CoreV1().Secrets(namespace).Get(ctx, pathContainerObject, metav1.GetOptions{})
		if err != nil {
			return nil, errors.Wrapf(err, "error reading secret %q from namespace %q", pathContainerObject, namespace)
		}

		data, ok := secret.Data[pathFile]
		if !ok {
			return nil, errors.Errorf("cluster file data %q not found in secret %s", pathFile, secret.Name)
		}

		return data, nil

	case "configmap":
		configMap, err := k8sClient.CoreV1().ConfigMaps(namespace).Get(ctx, pathContainerObject, metav1.GetOptions{})
		if err != nil {
			return nil, errors.Wrapf(err, "error reading configmap %q from namespace %q", pathContainerObject, namespace)
		}

		data, ok := configMap.BinaryData[pathFile]
		if !ok {
			dataStr, ok := configMap.Data[pathFile]
			if !ok {
				return nil, errors.Errorf("cluster file data %q not found in %#v", pathFile, configMap)
			}

			data = []byte(dataStr)
		}

		return data, nil

	default:
		return nil, errors.Errorf("the scheme %q in cluster file URL %q is not supported ", parsedURL.Scheme, urlAddress)
	}
}
