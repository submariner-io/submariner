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
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/types"
)

const (
	cableDriverName = "libreswan"
)

func init() {
	cable.AddDriver(cableDriverName, NewLibreswan)
	cable.SetDefaultCableDriver(cableDriverName)
}

type libreswan struct {
	secretKey string

	debug   bool
	logFile string

	localEndpoint types.SubmarinerEndpoint

	ipSecIKEPort  string
	ipSecNATTPort string

	// This tracks the requested connections
	connections []subv1.Connection
}

type specification struct {
	PSK      string
	Debug    bool
	LogFile  string
	IKEPort  string `default:"500"`
	NATTPort string `default:"4500"`
}

const defaultIKEPort = "500"
const defaultNATTPort = "4500"
const ipsecSpecEnvVarPrefix = "ce_ipsec"

// NewLibreswan starts an IKE daemon using Libreswan and configures it to manage Submariner's endpoints
func NewLibreswan(localEndpoint types.SubmarinerEndpoint, localCluster types.SubmarinerCluster) (cable.Driver, error) {
	ipSecSpec := specification{}

	err := envconfig.Process(ipsecSpecEnvVarPrefix, &ipSecSpec)
	if err != nil {
		return nil, fmt.Errorf("error processing environment config for %s: %v", ipsecSpecEnvVarPrefix, err)
	}

	return &libreswan{
		secretKey:     ipSecSpec.PSK,
		debug:         ipSecSpec.Debug,
		logFile:       ipSecSpec.LogFile,
		ipSecIKEPort:  ipSecSpec.IKEPort,
		ipSecNATTPort: ipSecSpec.NATTPort,
		localEndpoint: localEndpoint,
		connections:   []subv1.Connection{},
	}, nil
}

// GetName returns driver's name
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
		return fmt.Errorf("error creating the secrets file: %v", err)
	}
	defer file.Close()

	fmt.Fprintf(file, "%%any %%any : PSK \"%s\"\n", i.secretKey)

	// Ensure Pluto is started
	if err := i.runPluto(); err != nil {
		return fmt.Errorf("error starting Pluto: %v", err)
	}

	return nil
}

// Line format: 006 #3: "submariner-cable-cluster3-172-17-0-8-0-0", type=ESP, add_time=1590508783, inBytes=0, outBytes=0, id='172.17.0.8'
var trafficStatusRE = regexp.MustCompile(`.* "([^"]+)", .*inBytes=(\d+), outBytes=(\d+).*`)

func (i *libreswan) refreshConnectionStatus() error {
	// Retrieve active tunnels from the daemon
	cmd := exec.Command("/usr/libexec/ipsec/whack", "--trafficstatus")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.WithMessage(err, "error retrieving whack's stdout")
	}

	if err := cmd.Start(); err != nil {
		return errors.WithMessage(err, "error starting whack")
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

	if err := cmd.Wait(); err != nil {
		return errors.WithMessage(err, "error waiting for whack")
	}

	cable.RecordNoConnections()

	localSubnets := extractSubnets(i.localEndpoint.Spec)

	for j := range i.connections {
		isConnected := false

		remoteSubnets := extractSubnets(i.connections[j].Endpoint)
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

// GetActiveConnections returns an array of all the active connections for the given cluster.
func (i *libreswan) GetActiveConnections(clusterID string) ([]subv1.Connection, error) {
	return i.connections, nil
}

// GetConnections() returns an array of the existing connections, including status and endpoint info
func (i *libreswan) GetConnections() ([]subv1.Connection, error) {
	if err := i.refreshConnectionStatus(); err != nil {
		return []subv1.Connection{}, err
	}

	return i.connections, nil
}

func extractSubnets(endpoint subv1.EndpointSpec) []string {
	// Subnets
	subnets := []string{endpoint.PrivateIP + "/32"}

	for _, subnet := range endpoint.Subnets {
		if !strings.HasPrefix(subnet, endpoint.PrivateIP) {
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
		return fmt.Errorf("error whacking with args %v: %v", args, err)
	}

	return nil
}

// ConnectToEndpoint establishes a connection to the given endpoint and returns a string
// representation of the IP address of the target endpoint.
func (i *libreswan) ConnectToEndpoint(endpoint types.SubmarinerEndpoint, useIP string, useNAT bool) (string, error) {
	// This is the local endpoint’s IP address, which is always the private IP
	// (the IP needs to be assigned to a local interface; Libreswan uses that to determine
	// the tunnel’s orientation)
	localEndpointIP := i.localEndpoint.Spec.PrivateIP
	// The remote endpoint IP is the address the local system needs to connect to; if NAT
	// is involved, this will be the public IP, otherwise the private IP
	remoteEndpointIP := useIP
	// Identifiers are used for authentication, they’re always the private IPs
	localEndpointIdentifier := i.localEndpoint.Spec.PrivateIP
	remoteEndpointIdentifier := endpoint.Spec.PrivateIP
	leftSubnets := extractSubnets(i.localEndpoint.Spec)
	rightSubnets := extractSubnets(endpoint.Spec)

	// Ensure we’re listening
	if err := whack("--listen"); err != nil {
		return "", fmt.Errorf("error listening: %v", err)
	}

	klog.Infof("Creating connection(s) for %v", endpoint)

	if len(leftSubnets) > 0 && len(rightSubnets) > 0 {
		for lsi := range leftSubnets {
			for rsi := range rightSubnets {
				connectionName := fmt.Sprintf("%s-%d-%d", endpoint.Spec.CableName, lsi, rsi)

				args := []string{}

				args = append(args, "--psk", "--encrypt")
				if useNAT {
					args = append(args, "--forceencaps")
				}

				args = append(args, "--name", connectionName)

				// Left-hand side
				args = append(args, "--id", localEndpointIdentifier)
				args = append(args, "--host", localEndpointIP)
				args = append(args, "--client", leftSubnets[lsi])
				if useNAT {
					args = append(args, "--ikeport", i.ipSecNATTPort)
				} else {
					args = append(args, "--ikeport", i.ipSecIKEPort)
				}

				args = append(args, "--to")

				// Right-hand side
				args = append(args, "--id", remoteEndpointIdentifier)
				args = append(args, "--host", remoteEndpointIP)
				args = append(args, "--client", rightSubnets[rsi])
				if useNAT {
					args = append(args, "--ikeport", i.ipSecNATTPort)
				} else {
					args = append(args, "--ikeport", i.ipSecIKEPort)
				}

				klog.Infof("Executing whack with args: %v", args)

				if err := whack(args...); err != nil {
					return "", err
				}

				if err := whack("--route", "--name", connectionName); err != nil {
					return "", err
				}

				if err := whack("--initiate", "--asynchronous", "--name", connectionName); err != nil {
					return "", err
				}
			}
		}
	}

	i.connections = append(i.connections,
		subv1.Connection{Endpoint: endpoint.Spec, Status: subv1.Connected, UsingIP: useIP, UsingNAT: useNAT})
	cable.RecordConnection(cableDriverName, &i.localEndpoint.Spec, &endpoint.Spec, string(subv1.Connected), true)

	return remoteEndpointIP, nil
}

// DisconnectFromEndpoint disconnects from the connection to the given endpoint.
func (i *libreswan) DisconnectFromEndpoint(endpoint types.SubmarinerEndpoint) error {
	leftSubnets := extractSubnets(i.localEndpoint.Spec)
	rightSubnets := extractSubnets(endpoint.Spec)

	klog.Infof("Deleting connection to %v", endpoint)

	if len(leftSubnets) > 0 && len(rightSubnets) > 0 {
		for lsi := range leftSubnets {
			for rsi := range rightSubnets {
				connectionName := fmt.Sprintf("%s-%d-%d", endpoint.Spec.CableName, lsi, rsi)

				args := []string{}

				args = append(args, "--delete")
				args = append(args, "--name", connectionName)

				klog.Infof("Whacking with %v", args)

				cmd := exec.Command("/usr/libexec/ipsec/whack", args...)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				if err := cmd.Run(); err != nil {
					switch err := err.(type) {
					case *exec.ExitError:
						klog.Errorf("error deleting a connection with args %v; got exit code %d: %v", args, err.ExitCode(), err)
					default:
						return fmt.Errorf("error deleting a connection with args %v: %v", args, err)
					}
				}
			}
		}
	}

	i.connections = removeConnectionForEndpoint(i.connections, endpoint)
	cable.RecordDisconnected(cableDriverName, &i.localEndpoint.Spec, &endpoint.Spec)

	return nil
}

func removeConnectionForEndpoint(connections []subv1.Connection, endpoint types.SubmarinerEndpoint) []subv1.Connection {
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
		out, err := os.OpenFile(i.logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return fmt.Errorf("failed to open log file %s: %v", i.logFile, err)
		}

		cmd.Stdout = out
		cmd.Stderr = out
		outputFile = out
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}

	if err := cmd.Start(); err != nil {
		// Note - Close handles nil receiver
		outputFile.Close()
		return fmt.Errorf("error starting the Pluto process with args %v: %v", args, err)
	}

	go func() {
		defer outputFile.Close()
		klog.Fatalf("Pluto exited: %v", cmd.Wait())
	}()

	// Wait up to 5s for the control socket
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
