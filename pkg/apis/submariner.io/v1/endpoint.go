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
package v1

import (
	"strconv"

	"github.com/pkg/errors"
)

func (ep *EndpointSpec) GetBackendPort(configName string, defaultValue int32) (int32, error) {
	if portStr := ep.BackendConfig[configName]; portStr != "" {
		port, err := parsePort(portStr)
		if err != nil {
			return defaultValue, errors.Wrapf(err, "error parsing backend config %s", configName)
		}

		return port, nil
	}

	return defaultValue, nil
}

func parsePort(port string) (int32, error) {
	if portInt, err := strconv.ParseUint(port, 10, 16); err != nil {
		return -1, errors.Wrapf(err, "error parsing port %s", port)
	} else if portInt < 1 {
		return -1, errors.Errorf("port %s is < 1", port)
	} else if portInt > 65535 {
		return -1, errors.Errorf("port %s is > 65535", port)
	} else {
		return int32(portInt), nil
	}
}
