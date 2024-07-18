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

package libreswan

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/pkg/errors"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submendpoint "github.com/submariner-io/submariner/pkg/endpoint"
	"k8s.io/utils/ptr"
)

type operationMode int

const (
	operationModeBidirectional operationMode = iota
	operationModeServer
	operationModeClient
)

func (i *libreswan) calculateOperationMode(remoteEndpoint *v1.EndpointSpec) operationMode {
	leftPreferred, err := i.localEndpoint.GetBackendBool(v1.PreferredServerConfig, ptr.To(false))
	if err != nil {
		logger.Errorf(err, "Error parsing local endpoint config")
	}

	rightPreferred, err := remoteEndpoint.GetBackendBool(v1.PreferredServerConfig, nil)
	if err != nil {
		logger.Errorf(err, "Error parsing remote endpoint config %q", remoteEndpoint.CableName)
	}

	if rightPreferred == nil || !*leftPreferred && !*rightPreferred {
		return operationModeBidirectional
	}

	if *leftPreferred && !*rightPreferred {
		return operationModeServer
	}

	if *rightPreferred && !*leftPreferred {
		return operationModeClient
	}

	// At this point both would like to be server, so we decide based on the cable name
	if i.localEndpoint.CableName > remoteEndpoint.CableName {
		return operationModeServer
	}

	return operationModeClient
}

func (m operationMode) String() string {
	switch m {
	case operationModeBidirectional:
		return "bi-directional"
	case operationModeServer:
		return "server"
	case operationModeClient:
		return "client"
	default:
		return "unknown"
	}
}

func processPreferredServerConfig(localEndpoint *submendpoint.Local) error {
	preferredServerStr := localEndpoint.Spec().BackendConfig[v1.PreferredServerConfig]

	if preferredServerStr == "" {
		preferredServerStr = os.Getenv("CE_IPSEC_PREFERREDSERVER")
	}

	if preferredServerStr == "" {
		preferredServerStr = strconv.FormatBool(false)
	}

	preferredServer, err := strconv.ParseBool(preferredServerStr)
	if err != nil {
		return errors.Wrapf(err, "error parsing CE_IPSEC_PREFERREDSERVER or node gateway.submariner."+
			"io/preferred-server annotation bool: %s", preferredServerStr)
	}

	err = localEndpoint.Update(context.TODO(), func(existing *v1.EndpointSpec) {
		if existing.BackendConfig == nil {
			existing.BackendConfig = map[string]string{}
		}

		existing.BackendConfig[v1.PreferredServerConfig] = preferredServerStr

		// When this Endpoint is in "preferred-server" mode, a bogus timestamp is inserted in the BackendConfig,
		// forcing the resynchronization of the object to the broker, which will indicate to all clients that the
		// server has been restarted, and that they must re-connect to the endpoint.
		if preferredServer {
			existing.BackendConfig[v1.PreferredServerConfig+"-timestamp"] = strconv.FormatInt(time.Now().UTC().Unix(), 10)
		}
	})

	return errors.Wrap(err, "error updating local endpoint")
}
