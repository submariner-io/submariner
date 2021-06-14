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
package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Service controller", func() {
	t := newServiceControllerTestDriver()

	When("an exported Service is deleted", func() {
		BeforeEach(func() {
			t.createServiceExport(t.createService(newClusterIPService()))
			t.createGlobalIngressIP(&submarinerv1.GlobalIngressIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: serviceName,
				},
			})
		})

		It("should delete the GlobalIngressIP", func() {
			Expect(t.services.Delete(context.TODO(), serviceName, metav1.DeleteOptions{})).To(Succeed())
			t.awaitNoGlobalIngressIP(serviceName)
		})
	})
})

type serviceControllerTestDriver struct {
	*testDriverBase
}

func newServiceControllerTestDriver() *serviceControllerTestDriver {
	t := &serviceControllerTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()
	})

	JustBeforeEach(func() {
		t.start()
	})

	AfterEach(func() {
		t.testDriverBase.afterEach()
	})

	return t
}

func (t *serviceControllerTestDriver) start() {
	var err error

	t.controller, err = controllers.NewServiceController(syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	})

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}
