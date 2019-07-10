package ipsec

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/kelseyhightower/envconfig"
	"github.com/submariner-io/submariner/pkg/cableengine/ipsec/libreswan"
	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/klog"
)

type libreSwan struct {
	secretKey string

	debug   bool
	logFile string

	localEndpoint types.SubmarinerEndpoint
	localSubnets  []string

	// TODO Drop this
	connections []string
}

// NewLibreSwan starts an IKE daemon using LibreSwan and configures it to manage Submariner's endpoints
func NewLibreSwan(localSubnets []string, localEndpoint types.SubmarinerEndpoint) (Driver, error) {
	// TODO Extract the IPsec spec
	ipSecSpec := specification{}

	err := envconfig.Process("ce_ipsec", &ipSecSpec)
	if err != nil {
		return nil, fmt.Errorf("error processing environment config for ce_ipsec: %v", err)
	}

	return &libreSwan{
		secretKey:     ipSecSpec.PSK,
		debug:         ipSecSpec.Debug,
		logFile:       ipSecSpec.LogFile,
		localEndpoint: localEndpoint,
		localSubnets:  localSubnets,
		connections:   []string{},
	}, nil
}

func (i *libreSwan) Init() error {
	// Write the secrets file:
	// %any %any : PSK "secret"
	// TODO Check whether the file already exists
	file, err := os.Create("/etc/ipsec.d/submariner.secrets")
	if err != nil {
		return fmt.Errorf("Error creating the secrets file: %v", err)
	}
	defer file.Close()

	fmt.Fprintf(file, "%%any %%any : PSK \"%s\"\n", i.secretKey)

	// Ensure Pluto is started
	if err := runPluto(i.debug, i.logFile); err != nil {
		return fmt.Errorf("Error starting Pluto: %v", err)
	}
	return nil
}

func (i *libreSwan) GetActiveConnections(clusterID string) ([]string, error) {
	klog.V(4).Infof("Active connections: %v", i.connections)
	return i.connections, nil
}

func (i *libreSwan) ConnectToEndpoint(endpoint types.SubmarinerEndpoint) (string, error) {
	var localEndpointIP, remoteEndpointIP string

	if endpoint.Spec.NATEnabled {
		localEndpointIP = i.localEndpoint.Spec.PublicIP.String()
		remoteEndpointIP = endpoint.Spec.PublicIP.String()
	} else {
		localEndpointIP = i.localEndpoint.Spec.PrivateIP.String()
		remoteEndpointIP = endpoint.Spec.PrivateIP.String()
	}

	args := []string{}

	args = append(args, "--psk", "--encrypt")
	args = append(args, "--name", endpoint.Spec.CableName)

	// Left-hand side
	args = append(args, "--host", localEndpointIP)
	args = append(args, "--client")
	for _, subnet := range i.localSubnets {
		if !strings.HasPrefix(subnet, localEndpointIP) {
			args = append(args, subnet)
		}
	}

	args = append(args, "--to")

	// Right-hand side
	args = append(args, "--host", remoteEndpointIP)
	args = append(args, "--client")
	for _, subnet := range endpoint.Spec.Subnets {
		if !strings.HasPrefix(subnet, remoteEndpointIP) {
			args = append(args, subnet)
		}
	}

	klog.V(4).Infof("Creating connection to %v", endpoint)
	klog.V(4).Infof("Whacking with %v", args)

	cmd := exec.Command("/usr/libexec/ipsec/whack", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("Error adding a connection with args %v: %v", args, err)
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("Error defining connection: %v", err)
	}

	if err := libreswan.Route(endpoint.Spec.CableName); err != nil {
		return "", fmt.Errorf("Error routing connection %v: %v", endpoint.Spec.CableName, err)
	}

	if err := libreswan.Initiate(endpoint.Spec.CableName); err != nil {
		return "", fmt.Errorf("Error initiating connection %v: %v", endpoint.Spec.CableName, err)
	}

	i.connections = append(i.connections, endpoint.Spec.CableName)

	return remoteEndpointIP, nil
}

func (i *libreSwan) DisconnectFromEndpoint(endpoint types.SubmarinerEndpoint) error {
	return fmt.Errorf("Not implemented")
}

func runPluto(debug bool, logFile string) error {
	klog.Info("Starting Pluto")

	args := []string{}

	cmd := exec.Command("/usr/local/bin/pluto", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	var outputFile *os.File
	if logFile != "" {
		out, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return fmt.Errorf("Failed to open log file %s: %v", logFile, err)
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
		return fmt.Errorf("Error starting the Pluto process wih args %v: %v", args, err)
	}

	go func() {
		defer outputFile.Close()
		klog.Fatalf("Pluto exited: %v", cmd.Wait())
	}()

	return nil
}
