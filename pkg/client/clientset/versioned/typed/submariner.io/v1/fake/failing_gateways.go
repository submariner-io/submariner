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
package fake

import (
	"context"

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

func (f *FailingGateways) Create(ctx context.Context, g *v1.Gateway, options metav1.CreateOptions) (*v1.Gateway, error) {
	if f.FailOnCreate != nil {
		return nil, f.FailOnCreate
	}

	return f.GatewayInterface.Create(ctx, g, options)
}

func (f *FailingGateways) Update(ctx context.Context, g *v1.Gateway, options metav1.UpdateOptions) (*v1.Gateway, error) {
	if f.FailOnUpdate != nil {
		return nil, f.FailOnUpdate
	}

	return f.GatewayInterface.Update(ctx, g, options)
}

func (f *FailingGateways) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	if f.FailOnDelete != nil {
		return f.FailOnDelete
	}

	return f.GatewayInterface.Delete(ctx, name, options)
}

func (f *FailingGateways) Get(ctx context.Context, name string, options metav1.GetOptions) (*v1.Gateway, error) {
	if f.FailOnGet != nil {
		return nil, f.FailOnGet
	}

	return f.GatewayInterface.Get(ctx, name, options)
}

func (f *FailingGateways) List(ctx context.Context, opts metav1.ListOptions) (*v1.GatewayList, error) {
	if f.FailOnList != nil {
		return nil, f.FailOnList
	}

	return f.GatewayInterface.List(ctx, opts)
}
