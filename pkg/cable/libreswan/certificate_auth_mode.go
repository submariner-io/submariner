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
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/command"
	"github.com/submariner-io/admiral/pkg/log"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"k8s.io/apimachinery/pkg/util/sets"
	k8snet "k8s.io/utils/net"
)

// certModeReconcileThreshold is the number of consecutive status refreshes in which a connection must have
// no established IPsec SA before Pluto is queried for whether it still has the connection loaded. The
// refreshes are a few seconds apart, so this tolerates the brief windows in which a connection legitimately
// has no SA, such as while it's still being negotiated, and keeps the query off the healthy path.
const certModeReconcileThreshold = 3

func (i *libreswan) connectToEndpointCertMode(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	endpoint := &endpointInfo.Endpoint

	if err := i.installCertModeConnections(&endpoint.Spec, endpointInfo.UseIP, endpointInfo.UseFamily, endpointInfo.UseNAT,
		nil); err != nil {
		return "", err
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

// installCertModeConnections writes the Libreswan connection stanzas for the given remote endpoint and loads
// them into Pluto. It is idempotent: AppendConnectionStanza replaces any pre-existing stanza and "ipsec auto
// --add" replaces any pre-existing connection, so it can be re-run to restore connections that were dropped
// from the Pluto daemon that Submariner shares with the host in cert mode.
//
// If onlyConns is non-nil, only the connections it names are installed. Replacing a connection terminates
// any SA Pluto is negotiating for it, so a caller restoring a subset must not disturb the rest.
func (i *libreswan) installCertModeConnections(endpoint *subv1.EndpointSpec, useIP string, useFamily k8snet.IPFamily,
	useNAT bool, onlyConns sets.Set[string],
) error {
	leftID := ClientCertName
	left := i.localEndpoint.GetPrivateIP(useFamily)

	// Validate endpoint inputs to prevent config injection
	if err := validateEndpointInputs(endpoint, left, useIP); err != nil {
		return err
	}

	right := useIP

	leftSubnets := i.localEndpoint.ExtractSubnetsExcludingIP(useIP)
	rightSubnets := endpoint.ExtractSubnetsExcludingIP(useIP)

	for lsi, leftSubnet := range leftSubnets {
		for rsi, rightSubnet := range rightSubnets {
			connName := toConnectionName(endpoint.CableName, useFamily, lsi, rsi)

			if onlyConns != nil && !onlyConns.Has(connName) {
				continue
			}

			encapsulationLine := ""
			if useNAT || i.forceUDPEncapsulation {
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
				return errors.Wrapf(err, "failed to append connection stanza to %s", SubmarinerConfPath())
			}

			logger.Infof("Appended Libreswan connection config for %q to %s", connName, SubmarinerConfPath())

			output, err := command.New(exec.Command("ipsec", "auto", "--add", connName)).CombinedOutput()
			if err != nil {
				return errors.Wrapf(err, "failed to add connection with ipsec auto --add: %s", string(output))
			}

			logger.Infof("Added connection with \"ipsec auto --add\": %q", string(output))

			connectionMode := i.calculateOperationMode(endpoint)

			logger.Infof("Connection mode for %q: %v", connName, connectionMode)

			if connectionMode == operationModeClient || connectionMode == operationModeBidirectional {
				output, err = command.New(exec.Command("ipsec", "whack", "--name", connName, "--initiate")).CombinedOutput()
				if err != nil {
					return errors.Wrapf(err, "failed to bring up connection %s with whack: %s", connName, string(output))
				}

				logger.Infof("Brought up connection %q with whack: %s", connName, string(output))
			}
		}
	}

	return nil
}

func (i *libreswan) disconnectCertMode(connectionName string) error {
	if err := i.connectionFile.RemoveConnectionStanza(connectionName); err != nil {
		return errors.Wrapf(err, "failed to remove connection stanza for %q", connectionName)
	}

	logger.Infof("Removed Libreswan connection config for %q", connectionName)

	return nil
}

// Pluto prints a block of lines per loaded connection definition, each prefixed with the quoted connection
// name, eg:
// "submariner-cable-cluster3-172-17-0-8-v4-0-0": 10.0.0.0/16===192.68.1.1[%fromcert]...; routed-tunnel; ...
// "submariner-cable-cluster3-172-17-0-8-v4-0-0":   host: oriented; local: 192.68.1.1; remote: 172.17.0.8;
//
// Some Libreswan versions additionally prefix every line with a three-digit FTP-style status code, as
// TrafficStatusRE's examples show for "whack --trafficstatus", so tolerate an optional one.
//
// SA state lines are prefixed with "#<num>: " after any status code, so they don't match.

var LoadedConnectionRE = regexp.MustCompile(`^(?:\d{3} )?"([^"]+)":`)

// retrieveLoadedConnections returns the names of the connections currently loaded in Pluto. A connection may
// be loaded without having an established IPsec SA, ie without appearing in the "whack --trafficstatus"
// output, in which case Pluto is responsible for establishing it.
func retrieveLoadedConnections() (sets.Set[string], error) {
	ctx, cancel := context.WithTimeout(context.TODO(), whackTimeout)
	defer cancel()

	cmd := command.New(exec.CommandContext(ctx, "/usr/sbin/ipsec", "whack", "--status"))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.WithMessage(err, "error retrieving whack's stdout")
	}

	if err := cmd.Start(); err != nil {
		return nil, errors.WithMessage(err, "error starting whack")
	}

	loadedConnections := sets.New[string]()
	scanner := bufio.NewScanner(stdout)

	for scanner.Scan() {
		if matches := LoadedConnectionRE.FindStringSubmatch(scanner.Text()); matches != nil {
			loadedConnections.Insert(matches[1])
		}
	}

	return loadedConnections, errors.Wrap(cmd.Wait(), "error waiting for whack to complete")
}

func missedRefreshesKey(cableName string, family k8snet.IPFamily) string {
	return fmt.Sprintf("%s-v%s", cableName, family)
}

// reconcileCertModeConnections re-installs the connections that Pluto no longer has. In cert mode Submariner
// doesn't run its own Pluto daemon, it uses the one already running on the host, which may be shared with
// other users such as OVN-Kubernetes IPsec. When such a user reloads the daemon, Submariner's connections
// are dropped from it, and Pluto can't revive a connection it no longer knows about, so Submariner has to
// add it back itself. This doesn't apply to PSK mode where Submariner owns the Pluto daemon and configures
// DPD, so Pluto does recover on its own, hence this is only called in cert mode.
//
// A connection with no established IPsec SA isn't necessarily missing from Pluto - it may simply not be
// reachable yet, in which case Pluto is already retrying and re-installing would needlessly terminate the
// SAs it's negotiating. So Pluto is only queried, once, after a connection has been without an SA for
// certModeReconcileThreshold consecutive refreshes.
func (i *libreswan) reconcileCertModeConnections() {
	var stale []int

	for j := range i.connections {
		key := missedRefreshesKey(i.connections[j].Endpoint.CableName, i.connections[j].GetFamily())

		if i.connections[j].Status == subv1.Connected {
			delete(i.missedRefreshes, key)
			continue
		}

		i.missedRefreshes[key]++
		if i.missedRefreshes[key] >= certModeReconcileThreshold {
			delete(i.missedRefreshes, key)

			stale = append(stale, j)
		}
	}

	if len(stale) == 0 {
		return
	}

	loadedConnections, err := retrieveLoadedConnections()
	if err != nil {
		logger.Errorf(err, "Error retrieving the connections loaded in Pluto")
		return
	}

	for _, j := range stale {
		connection := &i.connections[j]
		family := connection.GetFamily()

		// Derive the subnets the same way installCertModeConnections does, ie by excluding the IP the cable
		// actually uses, so that the connection names generated here match the ones it installed.
		localSubnets := i.localEndpoint.ExtractSubnetsExcludingIP(connection.UsingIP)
		remoteSubnets := connection.Endpoint.ExtractSubnetsExcludingIP(connection.UsingIP)
		missing := sets.New[string]()

		for lsi := range localSubnets {
			for rsi := range remoteSubnets {
				if name := toConnectionName(connection.Endpoint.CableName, family, lsi, rsi); !loadedConnections.Has(name) {
					missing.Insert(name)
				}
			}
		}

		if missing.Len() == 0 {
			logger.V(log.DEBUG).Infof("Connection %q is still loaded in Pluto", connection.Endpoint.CableName)

			continue
		}

		logger.Warningf("Connection(s) %q are no longer loaded in Pluto - re-installing them", sets.List(missing))

		if err := i.installCertModeConnections(&connection.Endpoint, connection.UsingIP, family, connection.UsingNAT,
			missing); err != nil {
			logger.Errorf(err, "Error re-installing connection %q", connection.Endpoint.CableName)
		}
	}
}
