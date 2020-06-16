package libreswan

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/kelseyhightower/envconfig"
	"k8s.io/klog"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
)

const (
	cableDriverName = "libreswan"
)

func init() {
	cable.AddDriver(cableDriverName, NewLibreswan)
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

func (i *libreswan) refreshConnectionStatus() error {
	// Retrieve active tunnels from the daemon
	cmd := exec.Command("/usr/libexec/ipsec/whack", "--trafficstatus")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		klog.Errorf("error retrieving whack's stdout: %v", err)
		return err
	}
	if err := cmd.Start(); err != nil {
		klog.Errorf("error starting whack: %v", err)
		return err
	}
	scanner := bufio.NewScanner(stdout)
	activeConnections := util.NewStringSet()
	for scanner.Scan() {
		line := scanner.Text()
		// Line format: 006 #3: "submariner-cable-cluster3-172-17-0-8-0-0", type=ESP, add_time=1590508783, inBytes=0, outBytes=0, id='172.17.0.8'
		components := strings.Split(line, "\"")
		if len(components) == 3 {
			activeConnections.Add(components[1])
		}
	}
	if err := cmd.Wait(); err != nil {
		klog.Errorf("error waiting for whack: %v", err)
		return err
	}

	localSubnets := extractSubnets(i.localEndpoint.Spec)
	for j := range i.connections {
		allConnected := true
		remoteSubnets := extractSubnets(i.connections[j].Endpoint)
		for lsi := range localSubnets {
			for rsi := range remoteSubnets {
				connectionName := fmt.Sprintf("%s-%d-%d", i.connections[j].Endpoint.CableName, lsi, rsi)
				if !activeConnections.Contains(connectionName) {
					allConnected = false
				}
			}
		}
		if allConnected {
			i.connections[j].Status = subv1.Connected
		} else {
			// Pluto should be connecting for us
			i.connections[j].Status = subv1.Connecting
		}
	}

	return nil
}

// GetActiveConnections returns an array of all the active connections for the given cluster.
func (i *libreswan) GetActiveConnections(clusterID string) ([]string, error) {
	if err := i.refreshConnectionStatus(); err != nil {
		return []string{}, err
	}

	connections := []string{}
	for j := range i.connections {
		if i.connections[j].Status == subv1.Connected {
			connections = append(connections, i.connections[j].Endpoint.CableName)
		}
	}
	klog.Infof("Active connections: %v", connections)
	return connections, nil
}

// GetConnections() returns an array of the existing connections, including status and endpoint info
func (i *libreswan) GetConnections() (*[]subv1.Connection, error) {
	if err := i.refreshConnectionStatus(); err != nil {
		return &[]subv1.Connection{}, err
	}
	return &i.connections, nil
}

func extractEndpointIP(endpoint subv1.EndpointSpec) string {
	if endpoint.NATEnabled {
		return endpoint.PublicIP
	} else {
		return endpoint.PrivateIP
	}
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

// ConnectToEndpoint establishes a connection to the given endpoint and returns a string
// representation of the IP address of the target endpoint.
func (i *libreswan) ConnectToEndpoint(endpoint types.SubmarinerEndpoint) (string, error) {
	// This is the local endpoint’s IP address, which is always the private IP
	// (the IP needs to be assigned to a local interface; Libreswan uses that to determine
	// the tunnel’s orientation)
	localEndpointIP := i.localEndpoint.Spec.PrivateIP
	// The remote endpoint IP is the address the local system needs to connect to; if NAT
	// is involved, this will be the public IP, otherwise the private IP
	remoteEndpointIP := extractEndpointIP(endpoint.Spec)
	// Identifiers are used for authentication, they’re always the private IPs
	localEndpointIdentifier := i.localEndpoint.Spec.PrivateIP
	remoteEndpointIdentifier := endpoint.Spec.PrivateIP
	leftSubnets := extractSubnets(i.localEndpoint.Spec)
	rightSubnets := extractSubnets(endpoint.Spec)

	// Ensure we’re listening
	cmd := exec.Command("/usr/libexec/ipsec/whack", "--listen")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error listening: %v", err)
	}

	if len(leftSubnets) > 0 && len(rightSubnets) > 0 {
		for lsi := range leftSubnets {
			for rsi := range rightSubnets {
				connectionName := fmt.Sprintf("%s-%d-%d", endpoint.Spec.CableName, lsi, rsi)

				args := []string{}

				args = append(args, "--psk", "--encrypt")
				if endpoint.Spec.NATEnabled {
					args = append(args, "--forceencaps")
				}
				args = append(args, "--name", connectionName)

				// Left-hand side
				args = append(args, "--id", localEndpointIdentifier)
				args = append(args, "--host", localEndpointIP)
				args = append(args, "--client", leftSubnets[lsi])

				args = append(args, "--to")

				// Right-hand side
				args = append(args, "--id", remoteEndpointIdentifier)
				args = append(args, "--host", remoteEndpointIP)
				args = append(args, "--client", rightSubnets[rsi])

				klog.Infof("Creating connection to %v", endpoint)
				klog.Infof("Whacking with %v", args)

				cmd = exec.Command("/usr/libexec/ipsec/whack", args...)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				if err := cmd.Run(); err != nil {
					switch err := err.(type) {
					case *exec.ExitError:
						klog.Errorf("error adding a connection with args %v; got exit code %d: %v", args, err.ExitCode(), err)
					default:
						return "", fmt.Errorf("error adding a connection with args %v: %v", args, err)
					}
				}

				cmd = exec.Command("/usr/libexec/ipsec/whack", "--route", "--name", connectionName)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				if err := cmd.Run(); err != nil {
					klog.Errorf("error routing connection %s: %v", connectionName, err)
				}

				cmd = exec.Command("/usr/libexec/ipsec/whack", "--initiate", "--name", connectionName)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				if err := cmd.Run(); err != nil {
					klog.Errorf("error initiating a connection with args %v: %v", args, err)
				}
			}
		}
	}

	i.connections = append(i.connections, subv1.Connection{Endpoint: endpoint.Spec, Status: subv1.Connected})

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

	return nil
}

func removeConnectionForEndpoint(connections []subv1.Connection, endpoint types.SubmarinerEndpoint) []subv1.Connection {
	for j := range connections {
		if connections[j].Endpoint.CableName == endpoint.Spec.CableName {
			if j == 0 && j == len(connections)-1 {
				return []subv1.Connection{}
			} else if j == 0 {
				return connections[j+1:]
			} else if j == len(connections)-1 {
				return connections[:j]
			}
			return append(connections[:j], connections[j+1:]...)
		}
	}
	return connections
}

func (i *libreswan) runPluto() error {
	klog.Info("Starting Pluto")

	args := []string{}
	args = append(args, "--ikeport", i.ipSecIKEPort)
	args = append(args, "--natikeport", i.ipSecNATTPort)

	cmd := exec.Command("/usr/local/bin/pluto", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	var outputFile *os.File
	if i.logFile != "" {
		out, err := os.OpenFile(i.logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return fmt.Errorf("Failed to open log file %s: %v", i.logFile, err)
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
		return fmt.Errorf("error starting the Pluto process wih args %v: %v", args, err)
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

	return nil
}
