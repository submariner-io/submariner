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

import (
	"github.com/pkg/errors"

	goovn "github.com/ebay/go-ovn"
	"k8s.io/klog"
)

const nodeLocalSwitch = "node_local_switch"

func (ovn *SyncHandler) detectOvnKubernetesImplementation() error {
	ls, err := ovn.nbdb.LSGet(nodeLocalSwitch)

	switch {
	case ls != nil && err == nil:
		klog.Infof("New ovn-kubernetes implementation detected, using %q", submarinerUpstreamNETv2)

		ovn.submarinerUpstreamIP = SubmarinerUpstreamIPv2
		ovn.submarinerUpstreamNet = submarinerUpstreamNETv2
		ovn.hostUpstreamIP = hostUpstreamIPv2
		ovn.hasNodeLocalSwitch = true
	case ls == nil && errors.Is(err, goovn.ErrorNotFound):
		klog.Infof("Old ovn-kubernetes implementation detected, using %q", submarinerUpstreamNET)

		ovn.submarinerUpstreamIP = SubmarinerUpstreamIP
		ovn.submarinerUpstreamNet = submarinerUpstreamNET
		ovn.hostUpstreamIP = hostUpstreamIP
	default:
		return errors.Wrapf(err, "error trying to find %q switch to detect ovn version details", nodeLocalSwitch)
	}

	return nil
}
