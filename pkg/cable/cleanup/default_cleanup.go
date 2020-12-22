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
package cleanup

import (
	"github.com/submariner-io/submariner/pkg/cable/strongswan"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
)

// TODO(mangelajo) This is a generic GetCleanupHandlers as a first step, on a later step
//                 we should remove this and use the GetCleanupHandlers from the specific
//                 cable driver(s) in use

func GetCleanupHandlers() []cleanup.Handler {
	return []cleanup.Handler{
		NewRoutingTableCleanup(strongswan.DefaultRoutingTable),
		NewXFRMCleanupHandler(),
	}
}
