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

package endpoint_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakeClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	submarinerClientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/types"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const (
	UsingLoadBalancer = "using-loadbalancer"
	initialIP         = "1.2.3.4"
	updatedIP         = "4.3.2.1"
	clusterID         = "eastcluster"
	interval          = 50 * time.Millisecond
	testServiceName   = "my-loadbalancer"
	testNamespace     = "namespace"
)

var _ = Describe("public ip watcher", func() {
	var endpointName string
	var err error

	t := newPublicIPWatcherTestDriver()

	BeforeEach(func() {
		cableName := fmt.Sprintf("submariner-cable-%s-192-68-10-2", clusterID)

		// Let's create a local endpoint with load-balancer set to true but without any public-ip
		t.localEPSpec = newEndpointSpec(clusterID, cableName)

		endpointName, err = t.localEPSpec.GenerateName()
		Expect(err).To(Succeed())
	})

	AfterEach(func() {
		close(t.stopCh)
	})

	When("using the LoadBalancer mode and the Ingress IP is modified", func() {
		It("should update the public IP of local endpoint accordingly", func() {
			// Allow the public-ip watcher to resolve public-ip from the corresponding load-balancer service.
			time.Sleep(2 * interval)

			// Verify that local endpoint now has public-ip
			obj := t.getLocalEndpoint(endpointName)
			Expect(obj.Spec.PublicIP).To(Equal(initialIP))

			// Update the load-balancer ingress ip
			t.updateLoadbalancerService(testServiceName, testNamespace, updatedIP)
			time.Sleep(2 * interval)

			// Verify that local endpoint now has the updated public-ip
			obj = t.getLocalEndpoint(endpointName)
			Expect(obj.Spec.PublicIP).To(Equal(updatedIP))
		})
	})
})

type publicIPWatcherTestDriver struct {
	smEndpointClient submarinerClientsetv1.EndpointInterface
	k8sClient        *fake.Clientset
	localEPSpec      submarinerv1.EndpointSpec
	stopCh           chan struct{}
}

func newPublicIPWatcherTestDriver() *publicIPWatcherTestDriver {
	t := &publicIPWatcherTestDriver{}

	BeforeEach(func() {
		t.stopCh = make(chan struct{})
		t.k8sClient = fake.NewSimpleClientset(loadBalancerService(v1.LoadBalancerIngress{Hostname: "", IP: initialIP}))
		t.smEndpointClient = fakeClientset.NewSimpleClientset().SubmarinerV1().Endpoints(testNamespace)
	})

	JustBeforeEach(func() {
		t.createEndpoint(&t.localEPSpec)

		ipWatcher := endpoint.NewPublicIPWatcher(&endpoint.PublicIPWatcherConfig{
			SubmSpec: &types.SubmarinerSpecification{
				ClusterID: clusterID,
				Namespace: testNamespace,
				PublicIP:  "lb:" + testServiceName,
			},
			Interval:  interval,
			K8sClient: t.k8sClient,
			Endpoints: t.smEndpointClient,
			LocalEndpoint: types.SubmarinerEndpoint{
				Spec: t.localEPSpec,
			},
		})

		ipWatcher.Run(t.stopCh)
	})

	return t
}

func (t *publicIPWatcherTestDriver) createEndpoint(spec *submarinerv1.EndpointSpec) *submarinerv1.Endpoint {
	endpointName, err := spec.GenerateName()
	Expect(err).To(Succeed())

	ep := &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: *spec,
	}

	obj, err := t.smEndpointClient.Create(context.TODO(), ep, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	return obj
}

func (t *publicIPWatcherTestDriver) getLocalEndpoint(name string) *submarinerv1.Endpoint {
	obj, err := t.smEndpointClient.Get(context.TODO(), name, metav1.GetOptions{})
	Expect(err).To(Succeed())

	return obj
}

func newEndpointSpec(clusterID, cableName string) submarinerv1.EndpointSpec {
	return submarinerv1.EndpointSpec{
		CableName: cableName,
		ClusterID: clusterID,
		PrivateIP: "192-68-10-2",
		Hostname:  "myhost",
		Subnets:   []string{"10.1.0.0/24", "100.1.0.0/24"},
		BackendConfig: map[string]string{
			UsingLoadBalancer: "true",
		},
	}
}

func (t *publicIPWatcherTestDriver) updateLoadbalancerService(lbservice, ns, ip string) {
	ingress := []v1.LoadBalancerIngress{{Hostname: "", IP: ip}}
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lbservice,
			Namespace: ns,
		},
		Status: v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{
				Ingress: ingress,
			},
		},
	}

	_, err := t.k8sClient.CoreV1().Services(ns).Update(context.TODO(), svc, metav1.UpdateOptions{})
	Expect(err).To(Succeed())
}

func loadBalancerService(ingress ...v1.LoadBalancerIngress) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testServiceName,
			Namespace: testNamespace,
		},
		Status: v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{
				Ingress: ingress,
			},
		},
	}
}
