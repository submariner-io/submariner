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

package endpoint

import (
	"context"
	"time"

	"github.com/pkg/errors"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	v1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

type PublicIPWatcherConfig struct {
	SubmSpec      *types.SubmarinerSpecification
	Interval      time.Duration
	K8sClient     kubernetes.Interface
	Endpoints     v1.EndpointInterface
	LocalEndpoint types.SubmarinerEndpoint
}

type PublicIPWatcher struct {
	config PublicIPWatcherConfig
}

const DefaultMonitorInterval = 60 * time.Second

func NewPublicIPWatcher(config *PublicIPWatcherConfig) *PublicIPWatcher {
	controller := &PublicIPWatcher{
		config: *config,
	}

	if controller.config.Interval == 0 {
		controller.config.Interval = DefaultMonitorInterval
	}

	return controller
}

func (p *PublicIPWatcher) Run(stopCh <-chan struct{}) {
	logger.Info("Starting the public IP watcher.")

	go func() {
		wait.Until(p.syncPublicIP, p.config.Interval, stopCh)
	}()
}

func (p *PublicIPWatcher) syncPublicIP() {
	publicIP, err := getPublicIP(p.config.SubmSpec, p.config.K8sClient, p.config.LocalEndpoint.Spec.BackendConfig, false)
	if err != nil {
		logger.Warningf("Could not determine public IP of the gateway node %q: %v", p.config.LocalEndpoint.Spec.Hostname, err)
		return
	}

	if p.config.LocalEndpoint.Spec.PublicIP != publicIP {
		logger.Infof("Public IP changed for the Gateway, updating the local endpoint with publicIP %q", publicIP)

		if err := p.updateLocalEndpoint(publicIP); err != nil {
			logger.Error(err, "Error updating the public IP for local endpoint")
			return
		}
	}
}

func (p *PublicIPWatcher) updateLocalEndpoint(publicIP string) error {
	var ep *submv1.Endpoint

	endpointName, err := p.config.LocalEndpoint.Spec.GenerateName()
	if err != nil {
		return errors.Wrapf(err, "error extracting the submariner Endpoint name from %#v", p.config.LocalEndpoint)
	}

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ep, err = p.config.Endpoints.Get(context.TODO(), endpointName, metav1.GetOptions{})
		if err != nil {
			return errors.Wrapf(err, "unable to get endpoint %q", endpointName)
		}

		ep.Spec.PublicIP = publicIP

		_, updateErr := p.config.Endpoints.Update(context.TODO(), ep, metav1.UpdateOptions{})
		return updateErr //nolint:wrapcheck // We wrap it below in the enclosing function
	})

	if retryErr != nil {
		return errors.Wrapf(retryErr, "error updating the public IP of local endpoint %q", endpointName)
	}

	p.config.LocalEndpoint.Spec = ep.Spec

	return nil
}
