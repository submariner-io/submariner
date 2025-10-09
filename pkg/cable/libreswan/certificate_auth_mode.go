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
	"fmt"
	"os/exec"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/command"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
)

func (i *libreswan) connectToEndpointCertMode(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	endpoint := &endpointInfo.Endpoint
	leftID := ClientCertName
	left := i.localEndpoint.GetPrivateIP(endpointInfo.UseFamily)
	right := endpointInfo.UseIP

	leftSubnets := i.localEndpoint.ExtractSubnetsExcludingIP(endpointInfo.UseIP)
	rightSubnets := endpoint.Spec.ExtractSubnetsExcludingIP(endpointInfo.UseIP)

	for lsi, leftSubnet := range leftSubnets {
		for rsi, rightSubnet := range rightSubnets {
			connName := toConnectionName(endpoint.Spec.CableName, endpointInfo.UseFamily, lsi, rsi)

			encapsulationLine := ""
			if endpointInfo.UseNAT || i.forceUDPEncapsulation {
				encapsulationLine = "    encapsulation=yes\n"
			}

			conf := fmt.Sprintf(`conn %s
    left=%s
    leftid=%%fromcert
    leftcert=%s
    leftrsasigkey=%%cert
    leftsubnet=%s
    leftmodecfgclient=false
    right=%s
    rightid=%%fromcert
    rightsubnet=%s
%s    auto=add
    ikev2=insist
    authby=rsasig
    type=tunnel`,
				connName,
				left,
				leftID,
				leftSubnet,
				right,
				rightSubnet,
				encapsulationLine,
			)
			if err := i.connectionFile.AppendConnectionStanza(conf, connName); err != nil {
				return "", errors.Wrapf(err, "failed to append connection stanza to %s", SubmarinerConfPath())
			}

			logger.Infof("Appended Libreswan connection config for %q to %s", connName, SubmarinerConfPath())

			output, err := command.New(exec.Command("ipsec", "auto", "--add", connName)).CombinedOutput()
			if err != nil {
				return "", errors.Wrapf(err, "failed to add connection with ipsec auto --add: %s", string(output))
			}

			logger.Infof("Added connection with \"ipsec auto --add\": %q", string(output))

			connectionMode := i.calculateOperationMode(&endpoint.Spec)

			logger.Infof("Connection mode for %q: %v", connName, connectionMode)

			if connectionMode == operationModeClient || connectionMode == operationModeBidirectional {
				whackArgs := []string{"--name", connName, "--initiate"}
				//nolint:gosec // ipsec whack args are from trusted config
				output, err = command.New(exec.Command("ipsec", append([]string{"whack"}, whackArgs...)...)).CombinedOutput()
				if err != nil {
					return "", errors.Wrapf(err, "failed to bring up connection %s with whack: %s", connName, string(output))
				}

				logger.Infof("Brought up connection %q with whack: %s", connName, string(output))
			}
		}
	}

	i.connections = append(i.connections,
		subv1.Connection{
			Endpoint: endpoint.Spec,
			UsingIP:  endpointInfo.UseIP,
			UsingNAT: endpointInfo.UseNAT,
			Status:   subv1.Connected,
		})

	return endpointInfo.UseIP, nil
}

func (i *libreswan) disconnectCertMode(connectionName string) error {
	if err := i.connectionFile.RemoveConnectionStanza(connectionName); err != nil {
		return errors.Wrapf(err, "failed to remove connection stanza for %q", connectionName)
	}

	logger.Infof("Removed Libreswan connection config for %q", connectionName)

	return nil
}
