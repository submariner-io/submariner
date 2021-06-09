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
package iprules

import "github.com/submariner-io/admiral/pkg/stringset"

type clusterEgress struct {
	localSubnets stringset.Interface
}

func ForClusterEgress(localSubnets stringset.Interface) (Interface, error) {
	return &clusterEgress{
		localSubnets: localSubnets,
	}, nil
}

func (c *clusterEgress) AddRules(forIPs ...string) error {
	return nil
}

func (c *clusterEgress) RemoveRules(forIPs ...string) error {
	return nil
}
