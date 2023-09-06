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

import "time"

const (
	ovnK8sSubmarinerInterface     = "ovn-k8s-sub0"
	ovnK8sSubmarinerBridge        = "br-submariner"
	ovsDBTimeout                  = 20 * time.Second
	ovnCert                       = "secret://openshift-ovn-kubernetes/ovn-cert/tls.crt"
	ovnPrivKey                    = "secret://openshift-ovn-kubernetes/ovn-cert/tls.key"
	ovnCABundle                   = "configmap://openshift-ovn-kubernetes/ovn-ca/ca-bundle.crt"
	ovnKubeService                = "ovnkube-db"
	defaultOVNUnixSocket          = "unix:/var/run/openvswitch/ovnnb_db.sock"
	defaultOVNOpenshiftUnixSocket = "unix:/var/run/ovn-ic/ovnnb_db.sock"
	ovnNBDBDefaultPort            = 6641
	defaultOpenshiftOVNNBDB       = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9641"
)
