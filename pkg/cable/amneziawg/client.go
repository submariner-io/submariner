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

package amneziawg

import (
	"context"

	awgctrl "github.com/advanced-wg/awgctrl-go"
	"github.com/advanced-wg/awgctrl-go/wgtypes"
	"github.com/pkg/errors"
)

type Client interface {
	ConfigureDevice(name string, cfg wgtypes.Config) error
	Device(name string) (*wgtypes.Device, error)
	Close() error
}

type awgClient struct {
	client *awgctrl.Client
}

//nolint:gocritic // hugeParam: matches awgctrl.Client API
func (c *awgClient) ConfigureDevice(name string, cfg wgtypes.Config) error {
	return c.client.ConfigureDevice(context.Background(), name, cfg) //nolint:wrapcheck // Let the caller wrap it
}

func (c *awgClient) Device(name string) (*wgtypes.Device, error) {
	return c.client.Device(context.Background(), name) //nolint:wrapcheck // Let the caller wrap it
}

func (c *awgClient) Close() error {
	return c.client.Close() //nolint:wrapcheck // Let the caller wrap it
}

var NewClient = func() (Client, error) {
	client, err := awgctrl.New()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create awgctrl client")
	}

	return &awgClient{client: client}, nil
}
