/*
© 2021 Red Hat, Inc. and others

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
package fake

import (
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FailingGateways struct {
	submarinerClientsetv1.GatewayInterface

	FailOnCreate error
	FailOnUpdate error
	FailOnDelete error
	FailOnGet    error
	FailOnList   error
}

func (f *FailingGateways) Create(g *v1.Gateway) (*v1.Gateway, error) {
	if f.FailOnCreate != nil {
		return nil, f.FailOnCreate
	}

	return f.GatewayInterface.Create(g)
}

func (f *FailingGateways) Update(g *v1.Gateway) (*v1.Gateway, error) {
	if f.FailOnUpdate != nil {
		return nil, f.FailOnUpdate
	}

	return f.GatewayInterface.Update(g)
}

func (f *FailingGateways) Delete(name string, options *metav1.DeleteOptions) error {
	if f.FailOnDelete != nil {
		return f.FailOnDelete
	}

	return f.GatewayInterface.Delete(name, options)
}

func (f *FailingGateways) Get(name string, options metav1.GetOptions) (*v1.Gateway, error) {
	if f.FailOnGet != nil {
		return nil, f.FailOnGet
	}

	return f.GatewayInterface.Get(name, options)
}

func (f *FailingGateways) List(opts metav1.ListOptions) (*v1.GatewayList, error) {
	if f.FailOnList != nil {
		return nil, f.FailOnList
	}

	return f.GatewayInterface.List(opts)
}
