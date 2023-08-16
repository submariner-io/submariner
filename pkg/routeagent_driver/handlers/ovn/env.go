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

import (
	"os"
	"time"
)

// default OVSDB timeout used by ovn-k.
const (
	OVSDBTimeout   = 20 * time.Second
	ovnCert        = "secret://openshift-ovn-kubernetes/ovn-cert/tls.crt"
	ovnPrivKey     = "secret://openshift-ovn-kubernetes/ovn-cert/tls.key"
	ovnCABundle    = "configmap://openshift-ovn-kubernetes/ovn-ca/ca-bundle.crt"
	defaultOVNNBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9641"
)

func getOVNNBDBAddress() string {
	return getEnvOr("OVN_NBDB", defaultOVNNBDB)
}

func getOVNPrivKeyPath() string {
	return getEnvOr("OVN_PK", ovnPrivKey)
}

func getOVNCertPath() string {
	return getEnvOr("OVN_CERT", ovnCert)
}

func getOVNCaBundlePath() string {
	return getEnvOr("OVN_CA", ovnCABundle)
}

func getEnvOr(key, defaultValue string) string {
	s := os.Getenv(key)
	if s == "" {
		logger.Infof("Using default value %q for %q", defaultValue, key)
		return defaultValue
	}

	return s
}
