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
	"strings"

	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/libovsdbops"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/sbdb"
	"github.com/pkg/errors"
)

func (ovn *SyncHandler) findChassisByHostname(hostname string) (*sbdb.Chassis, error) {
	chassisList, err := libovsdbops.ListChassis(ovn.sbdb)
	if err != nil {
		return nil, errors.Wrap(err, "unable to get chassis list from OVN")
	}
	// Attempt matching by the exact hostname.
	for i := range chassisList {
		if chassisList[i].Hostname == hostname {
			return chassisList[i], nil
		}
	}

	// In a second round try to match expecting a higher level domain after the hostname.
	for i := range chassisList {
		if strings.HasPrefix(chassisList[i].Hostname, hostname+".") ||
			strings.HasPrefix(hostname, chassisList[i].Hostname+".") {
			return chassisList[i], nil
		}
	}

	return nil, libovsdbclient.ErrNotFound
}
