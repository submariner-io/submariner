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
package ovn

import "os"

const (
	ovnCert        = "secret://openshift-ovn-kubernetes/ovn-cert/tls.crt"
	ovnPrivKey     = "secret://openshift-ovn-kubernetes/ovn-cert/tls.key"
	ovnCABundle    = "configmap://openshift-ovn-kubernetes/ovn-ca/ca-bundle.crt"
	defaultOVNNBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9641"
	defaultOVNSBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9642"
)

func getOVNSBDBAddress() string {
	addr := os.Getenv("OVN_SBDB")
	if addr == "" {
		return defaultOVNSBDB
	}

	return addr
}

func getOVNNBDBAddress() string {
	addr := os.Getenv("OVN_NBDB")
	if addr == "" {
		return defaultOVNNBDB
	}

	return addr
}

func getOVNPrivKeyPath() string {
	key := os.Getenv("OVN_PK")
	if key == "" {
		return ovnPrivKey
	}

	return key
}

func getOVNCertPath() string {
	cert := os.Getenv("OVN_CERT")
	if cert == "" {
		return ovnCert
	}

	return cert
}

func getOVNCaBundlePath() string {
	ca := os.Getenv("OVN_CA")
	if ca == "" {
		return ovnCABundle
	}

	return ca
}
