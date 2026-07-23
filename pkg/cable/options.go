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

package cable

import (
	"encoding/json"
	"os"

	"github.com/pkg/errors"
)

// CableDriverOptionsEnv is set by the operator from Submariner.spec.cableDriverOptions.
const CableDriverOptionsEnv = "SUBMARINER_CABLEDRIVEROPTIONS"

// GetDriverOptions returns driver-specific options from the environment.
// An empty value yields an empty map. Invalid JSON is returned as an error so
// misconfiguration fails fast instead of being silently ignored.
func GetDriverOptions() (map[string]string, error) {
	raw := os.Getenv(CableDriverOptionsEnv)
	if raw == "" {
		return map[string]string{}, nil
	}

	options := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return nil, errors.Wrapf(err, "error parsing %q", CableDriverOptionsEnv)
	}

	return options, nil
}
