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

package ovn

import "github.com/submariner-io/admiral/pkg/stringset"

func (ovn *Handler) getRemoteSubnets() stringset.Interface {
	endpointSubnets := stringset.New()

	for _, endpoint := range ovn.remoteEndpoints {
		for _, subnet := range endpoint.Spec.Subnets {
			endpointSubnets.Add(subnet)
		}
	}

	return endpointSubnets
}
