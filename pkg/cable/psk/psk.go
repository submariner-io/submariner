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

package psk

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
)

// Resolve returns the PSK to use, prioritizing PSKSecret over PSK.
// If pskSecret is non-empty, it reads from the secret file and base64 encodes it.
// Otherwise, it returns the psk value directly.
// rootDir is the root directory for reading secrets, primarily used for testing.
func Resolve(psk, pskSecret, rootDir string) (string, error) {
	if pskSecret == "" {
		return psk, nil
	}

	pskBytes, err := os.ReadFile(rootDir + fmt.Sprintf("/var/run/secrets/submariner.io/%s/psk", pskSecret))
	if err != nil {
		return "", errors.Wrapf(err, "error reading secret %s", pskSecret)
	}

	var encodedPsk strings.Builder
	encoder := base64.NewEncoder(base64.StdEncoding, &encodedPsk)

	if _, err := encoder.Write(pskBytes); err != nil {
		return "", errors.Wrap(err, "error encoding secret")
	}

	encoder.Close()

	return encodedPsk.String(), nil
}
