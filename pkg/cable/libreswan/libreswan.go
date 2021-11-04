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
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/klog"
)

const (
	cableDriverName = "libreswan"
)

func init() {
	cable.AddDriver(cableDriverName, NewLibreswan)
	cable.SetDefaultCableDriver(cableDriverName)
}

type libreswan struct {
	localEndpoint types.SubmarinerEndpoint
	// This tracks the requested connections
	connections []subv1.Connection

	secretKey string
	logFile   string

	ipSecNATTPort   string
	defaultNATTPort int32

	debug                 bool
	forceUDPEncapsulation bool
}

type specification struct {
	Debug       bool
	ForceEncaps bool
	PSK         string
	LogFile     string
	NATTPort    string `default:"4500"`
}

const defaultNATTPort = "4500"
const ipsecSpecEnvVarPrefix = "ce_ipsec"

// NewLibreswan starts an IKE daemon using Libreswan and configures it to manage Submariner's endpoints.
func NewLibreswan(localEndpoint *types.SubmarinerEndpoint, localCluster *types.SubmarinerCluster) (cable.Driver, error) {
	// We'll panic if localEndpoint or localCluster are nil, this is intentional
	ipSecSpec := specification{}

	err := envconfig.Process(ipsecSpecEnvVarPrefix, &ipSecSpec)
	if err != nil {
		return nil, errors.Wrapf(err, "error processing environment config for %s", ipsecSpecEnvVarPrefix)
	}

	defaultNATTPort, err := strconv.ParseUint(ipSecSpec.NATTPort, 10, 16)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing CR_IPSEC_NATTPORT environment variable")
	}

	nattPort, err := localEndpoint.Spec.GetBackendPort(subv1.UDPPortConfig, int32(defaultNATTPort))
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing %q from local endpoint", subv1.UDPPortConfig)
	}

	klog.Infof("Using NATT UDP port %d", nattPort)

	return &libreswan{
		secretKey:             ipSecSpec.PSK,
		debug:                 ipSecSpec.Debug,
		logFile:               ipSecSpec.LogFile,
		ipSecNATTPort:         strconv.Itoa(int(nattPort)),
		defaultNATTPort:       int32(defaultNATTPort),
		localEndpoint:         *localEndpoint,
		connections:           []subv1.Connection{},
		forceUDPEncapsulation: ipSecSpec.ForceEncaps,
	}, nil
}

// GetName returns driver's name.
func (i *libreswan) GetName() string {
	return cableDriverName
}

// Init initializes the driver with any state it needs.
func (i *libreswan) Init() error {
	// Write the secrets file:
	// %any %any : PSK "secret"
	// TODO Check whether the file already exists
	file, err := os.Create("/etc/ipsec.d/submariner.secrets")
	if err != nil {
		return errors.Wrap(err, "error creating the secrets file")
	}
	defer file.Close()

	fmt.Fprintf(file, "%%any %%any : PSK \"%s\"\n", i.secretKey)

	// Ensure Pluto is started
	if err := i.runPluto(); err != nil {
		return errors.Wrap(err, "error starting Pluto")
	}

	return nil
}

// Line format: 006 #3: "submariner-cable-cluster3-172-17-0-8-0-0", type=ESP, add_time=1590508783, inBytes=0, outBytes=0, id='172.17.0.8'
//          or: 006 #2: "submariner-cable-cluster3-172-17-0-8-0-0"[1] 3.139.75.179, type=ESP, add_time=1617195756, inBytes=0, outBytes=0,
//                        id='@10.0.63.203-0-0'"
var trafficStatusRE = regexp.MustCompile(`.* "([^"]+)"[^,]*, .*inBytes=(\d+), outBytes=(\d+).*`)

func retrieveActiveConnectionStats() (map[string]int, map[string]int, error) {
	// Retrieve active tunnels from the daemon
	cmd := exec.Command("/usr/libexec/ipsec/whack", "--trafficstatus")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, errors.WithMessage(err, "error retrieving whack's stdout")
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, errors.WithMessage(err, "error starting whack")
	}

	scanner := bufio.NewScanner(stdout)
	activeConnectionsRx := make(map[string]int)
	activeConnectionsTx := make(map[string]int)

	for scanner.Scan() {
		line := scanner.Text()
		matches := trafficStatusRE.FindStringSubmatch(line)
		if matches != nil {
			_, ok := activeConnectionsRx[matches[1]]
			if !ok {
				activeConnectionsRx[matches[1]] = 0
			}

			_, ok = activeConnectionsTx[matches[1]]
			if !ok {
				activeConnectionsTx[matches[1]] = 0
			}

			inBytes, err := strconv.Atoi(matches[2])
			if err != nil {
				klog.Warningf("Invalid inBytes in whack output line: %q", line)
			} else {
				activeConnectionsRx[matches[1]] += inBytes
			}

			outBytes, err := strconv.Atoi(matches[3])
			if err != nil {
				klog.Warningf("Invalid outBytes in whack output line: %q", line)
			} else {
				activeConnectionsTx[matches[1]] += outBytes
			}
		} else {
			klog.V(log.DEBUG).Infof("Ignoring whack output line: %q", line)
		}
	}

	return activeConnectionsRx, activeConnectionsTx, nil
}

func (i *libreswan) refreshConnectionStatus() error {
	activeConnectionsRx, activeConnectionsTx, err := retrieveActiveConnectionStats()
	if err != nil {
		return err
	}

	cable.RecordNoConnections()

	localSubnets := extractSubnets(&i.localEndpoint.Spec)

	for j := range i.connections {
		isConnected := false

		remoteSubnets := extractSubnets(&i.connections[j].Endpoint)
		rx, tx := 0, 0
		for lsi := range localSubnets {
			for rsi := range remoteSubnets {
				connectionName := fmt.Sprintf("%s-%d-%d", i.connections[j].Endpoint.CableName, lsi, rsi)
				subRx, okRx := activeConnectionsRx[connectionName]
				subTx, okTx := activeConnectionsTx[connectionName]
				if okRx || okTx {
					i.connections[j].Status = subv1.Connected
					isConnected = true
					rx += subRx
					tx += subTx
				} else {
					klog.V(log.DEBUG).Infof("Connection %q not found in active connections obtained from whack: %v, %v",
						connectionName, activeConnectionsRx, activeConnectionsTx)
				}
			}
		}

		cable.RecordConnection(cableDriverName, &i.localEndpoint.Spec, &i.connections[j].Endpoint, string(i.connections[j].Status), false)
		cable.RecordRxBytes(cableDriverName, &i.localEndpoint.Spec, &i.connections[j].Endpoint, rx)
		cable.RecordTxBytes(cableDriverName, &i.localEndpoint.Spec, &i.connections[j].Endpoint, tx)

		if !isConnected {
			// Pluto should be connecting for us
			i.connections[j].Status = subv1.Connecting
			cable.RecordConnection(cableDriverName, &i.localEndpoint.Spec, &i.connections[j].Endpoint, string(i.connections[j].Status), false)
			klog.V(log.DEBUG).Infof("Connection %q not found in active connections obtained from whack: %v, %v",
				i.connections[j].Endpoint.CableName, activeConnectionsRx, activeConnectionsTx)
		}
	}

	return nil
}

// GetActiveConnections returns an array of all the active connections.
func (i *libreswan) GetActiveConnections() ([]subv1.Connection, error) {
	return i.connections, nil
}

// GetConnections() returns an array of the existing connections, including status and endpoint info.
func (i *libreswan) GetConnections() ([]subv1.Connection, error) {
	if err := i.refreshConnectionStatus(); err != nil {
		return []subv1.Connection{}, err
	}

	return i.connections, nil
}

func extractSubnets(endpoint *subv1.EndpointSpec) []string {
	subnets := make([]string, 0, len(endpoint.Subnets))

	for _, subnet := range endpoint.Subnets {
		if !strings.HasPrefix(subnet, endpoint.PrivateIP+"/") {
			subnets = append(subnets, subnet)
		}
	}

	return subnets
}

func whack(args ...string) error {
	var err error

	for i := 0; i < 3; i++ {
		cmd := exec.Command("/usr/libexec/ipsec/whack", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		klog.V(log.TRACE).Infof("Whacking with %v", args)

		if err = cmd.Run(); err == nil {
			break
		}

		klog.Warningf("error %v whacking with args: %v", err, args)
		time.Sleep(1 * time.Second)
	}

	if err != nil {
		return errors.Wrapf(err, "error whacking with args %v", args)
	}

	return nil
}

// ConnectToEndpoint establishes a connection to the given endpoint and returns a string
// representation of the IP address of the target endpoint.
func (i *libreswan) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	// We'll panic if endpointInfo is nil, this is intentional
	endpoint := &endpointInfo.Endpoint

	rightNATTPort, err := endpoint.Spec.GetBackendPort(subv1.UDPPortConfig, i.defaultNATTPort)
	if err != nil {
		klog.Warningf("Error parsing %q from remote endpoint %q - using port %d instead: %v", subv1.UDPPortConfig,
			endpoint.Spec.CableName, i.defaultNATTPort, err)
	}

	leftSubnets := extractSubnets(&i.localEndpoint.Spec)
	rightSubnets := extractSubnets(&endpoint.Spec)

	// Ensure we’re listening
	if err := whack("--listen"); err != nil {
		return "", errors.Wrap(err, "error listening")
	}

	connectionMode := i.calculateOperationMode(&endpoint.Spec)

	klog.Infof("Creating connection(s) for %v in %s mode", endpoint, connectionMode)

	if len(leftSubnets) > 0 && len(rightSubnets) > 0 {
		for lsi, leftSubnet := range leftSubnets {
			for rsi, rightSubnet := range rightSubnets {
				connectionName := fmt.Sprintf("%s-%d-%d", endpoint.Spec.CableName, lsi, rsi)

				switch connectionMode {
				case operationModeBidirectional:
					err = i.bidirectionalConnectToEndpoint(connectionName, endpointInfo, leftSubnet, rightSubnet, rightNATTPort)
				case operationModeServer:
					err = i.serverConnectToEndpoint(connectionName, endpointInfo, leftSubnet, rightSubnet, lsi, rsi)
				case operationModeClient:
					err = i.clientConnectToEndpoint(connectionName, endpointInfo, leftSubnet, rightSubnet, rightNATTPort, lsi, rsi)
				}

				if err != nil {
					return "", err
				}
			}
		}
	}

	i.connections = append(i.connections,
		subv1.Connection{Endpoint: endpoint.Spec, Status: subv1.Connected, UsingIP: endpointInfo.UseIP, UsingNAT: endpointInfo.UseNAT})
	cable.RecordConnection(cableDriverName, &i.localEndpoint.Spec, &endpoint.Spec, string(subv1.Connected), true)

	return endpointInfo.UseIP, nil
}

func (i *libreswan) bidirectionalConnectToEndpoint(connectionName string, endpointInfo *natdiscovery.NATEndpointInfo,
	leftSubnet, rightSubnet string, rightNATTPort int32) error {
	// Identifiers are used for authentication, they’re always the private IPs
	localEndpointIdentifier := i.localEndpoint.Spec.PrivateIP
	remoteEndpointIdentifier := endpointInfo.Endpoint.Spec.PrivateIP

	args := []string{}

	args = append(args, "--psk", "--encrypt")
	if endpointInfo.UseNAT || i.forceUDPEncapsulation {
		args = append(args, "--forceencaps")
	}

	args = append(args, "--name", connectionName,

		// Left-hand side
		"--id", localEndpointIdentifier,
		"--host", i.localEndpoint.Spec.PrivateIP,
		"--client", leftSubnet,

		"--ikeport", i.ipSecNATTPort,

		"--to",

		// Right-hand side
		"--id", remoteEndpointIdentifier,
		"--host", endpointInfo.UseIP,
		"--client", rightSubnet,

		"--ikeport", strconv.Itoa(int(rightNATTPort)))

	klog.Infof("Executing whack with args: %v", args)

	if err := whack(args...); err != nil {
		return err
	}

	if err := whack("--route", "--name", connectionName); err != nil {
		return err
	}

	if err := whack("--initiate", "--asynchronous", "--name", connectionName); err != nil {
		return err
	}

	return nil
}

func (i *libreswan) serverConnectToEndpoint(connectionName string, endpointInfo *natdiscovery.NATEndpointInfo,
	leftSubnet, rightSubnet string, lsi, rsi int) error {
	localEndpointIdentifier := fmt.Sprintf("@%s-%d-%d", i.localEndpoint.Spec.PrivateIP, lsi, rsi)
	remoteEndpointIdentifier := fmt.Sprintf("@%s-%d-%d", endpointInfo.Endpoint.Spec.PrivateIP, rsi, lsi)

	args := []string{}

	args = append(args, "--psk", "--encrypt")
	if endpointInfo.UseNAT || i.forceUDPEncapsulation {
		args = append(args, "--forceencaps")
	}

	args = append(args, "--name", connectionName,

		// Left-hand side.
		"--id", localEndpointIdentifier,
		"--host", i.localEndpoint.Spec.PrivateIP,
		"--client", leftSubnet,

		"--ikeport", i.ipSecNATTPort,

		"--to",

		// Right-hand side.
		"--id", remoteEndpointIdentifier,
		"--host", "%any",
		"--client", rightSubnet)

	klog.Infof("Executing whack with args: %v", args)

	if err := whack(args...); err != nil {
		return err
	}

	// NOTE: in this case we don't route or initiate connection, we simply wait for the client
	// to connect from %any IP, using the right PSK & ID.
	return nil
}

func (i *libreswan) clientConnectToEndpoint(connectionName string, endpointInfo *natdiscovery.NATEndpointInfo,
	leftSubnet, rightSubnet string, rightNATTPort int32, lsi, rsi int) error {
	// Identifiers are used for authentication, they’re always the private IPs.
	localEndpointIdentifier := fmt.Sprintf("@%s-%d-%d", i.localEndpoint.Spec.PrivateIP, lsi, rsi)
	remoteEndpointIdentifier := fmt.Sprintf("@%s-%d-%d", endpointInfo.Endpoint.Spec.PrivateIP, rsi, lsi)

	args := []string{}

	args = append(args, "--psk", "--encrypt")
	if endpointInfo.UseNAT || i.forceUDPEncapsulation {
		args = append(args, "--forceencaps")
	}

	args = append(args, "--name", connectionName,

		// Left-hand side
		"--id", localEndpointIdentifier,
		"--host", i.localEndpoint.Spec.PrivateIP,
		"--client", leftSubnet,

		"--to",

		// Right-hand side
		"--id", remoteEndpointIdentifier,
		"--host", endpointInfo.UseIP,
		"--client", rightSubnet,

		"--ikeport", strconv.Itoa(int(rightNATTPort)))

	klog.Infof("Executing whack with args: %v", args)

	if err := whack(args...); err != nil {
		return err
	}

	if err := whack("--route", "--name", connectionName); err != nil {
		return err
	}

	if err := whack("--initiate", "--asynchronous", "--name", connectionName); err != nil {
		return err
	}

	return nil
}

// DisconnectFromEndpoint disconnects from the connection to the given endpoint.
func (i *libreswan) DisconnectFromEndpoint(endpoint *types.SubmarinerEndpoint) error {
	// We'll panic if endpoint is nil, this is intentional
	leftSubnets := extractSubnets(&i.localEndpoint.Spec)
	rightSubnets := extractSubnets(&endpoint.Spec)

	klog.Infof("Deleting connection to %v", endpoint)

	if len(leftSubnets) > 0 && len(rightSubnets) > 0 {
		for lsi := range leftSubnets {
			for rsi := range rightSubnets {
				connectionName := fmt.Sprintf("%s-%d-%d", endpoint.Spec.CableName, lsi, rsi)

				args := []string{}

				args = append(args, "--delete",
					"--name", connectionName)

				klog.Infof("Whacking with %v", args)

				cmd := exec.Command("/usr/libexec/ipsec/whack", args...)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				if err := cmd.Run(); err != nil {
					var exitError *exec.ExitError
					if errors.As(err, &exitError) {
						klog.Errorf("error deleting a connection with args %v; got exit code %d: %v", args, exitError.ExitCode(), err)
					} else {
						return errors.Wrapf(err, "error deleting a connection with args %v", args)
					}
				}
			}
		}
	}

	i.connections = removeConnectionForEndpoint(i.connections, endpoint)
	cable.RecordDisconnected(cableDriverName, &i.localEndpoint.Spec, &endpoint.Spec)

	return nil
}

func removeConnectionForEndpoint(connections []subv1.Connection, endpoint *types.SubmarinerEndpoint) []subv1.Connection {
	for j := range connections {
		if connections[j].Endpoint.CableName == endpoint.Spec.CableName {
			copy(connections[j:], connections[j+1:])
			return connections[:len(connections)-1]
		}
	}

	return connections
}

func (i *libreswan) runPluto() error {
	klog.Info("Starting Pluto")

	args := []string{}

	if i.debug {
		args = append(args, "--stderrlog")
	}

	cmd := exec.Command("/usr/local/bin/pluto", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	var outputFile *os.File

	if i.logFile != "" {
		out, err := os.OpenFile(i.logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o666)
		if err != nil {
			return errors.Wrapf(err, "failed to open log file %s", i.logFile)
		}

		cmd.Stdout = out
		cmd.Stderr = out
		outputFile = out
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}

	if err := cmd.Start(); err != nil {
		// Note - Close handles nil receiver.
		outputFile.Close()
		return errors.Wrapf(err, "error starting the Pluto process with args %v", args)
	}

	go func() {
		defer outputFile.Close()
		klog.Fatalf("Pluto exited: %v", cmd.Wait())
	}()

	// Wait up to 5s for the control socket.
	for i := 0; i < 5; i++ {
		_, err := os.Stat("/run/pluto/pluto.ctl")
		if err == nil {
			break
		}

		if !os.IsNotExist(err) {
			klog.Infof("Failed to stat the control socket: %v", err)
			break
		}

		time.Sleep(1 * time.Second)
	}

	if i.debug {
		if err := whack("--debug", "base"); err != nil {
			return err
		}
	}

	return nil
}
