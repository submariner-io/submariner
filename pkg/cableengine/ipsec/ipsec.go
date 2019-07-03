package ipsec

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bronze1man/goStrongswanVici"
	"github.com/coreos/go-iptables/iptables"
	"github.com/kelseyhightower/envconfig"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/klog"
)

const (
	// DefaultReplayWindowSize specifies the replay window size for charon
	DefaultReplayWindowSize = "1024"

	// DefaultIkeSaRekeyInterval specifies the default rekey interval for IKE_SA
	DefaultIkeSaRekeyInterval = "4h"

	// DefaultChildSaRekeyInterval specifies the default rekey interval for CHILD_SA
	DefaultChildSaRekeyInterval = "1h"
)

type engine struct {
	sync.Mutex

	localSubnets              []string
	localCluster              types.SubmarinerCluster
	localEndpoint             types.SubmarinerEndpoint
	secretKey                 string
	replayWindowSize          string
	ipSecIkeSaRekeyInterval   string
	ipSecChildSaRekeyInterval string

	debug   bool
	logFile string
}

type specification struct {
	PSK     string
	Debug   bool
	LogFile string
}

func NewEngine(localSubnets []string, localCluster types.SubmarinerCluster, localEndpoint types.SubmarinerEndpoint) (cableengine.Engine, error) {

	ipSecSpec := specification{}

	err := envconfig.Process("ce_ipsec", &ipSecSpec)
	if err != nil {
		return nil, fmt.Errorf("error processing environment config for ce_ipsec: %v", err)
	}

	return &engine{
		replayWindowSize:          DefaultReplayWindowSize,
		ipSecIkeSaRekeyInterval:   DefaultIkeSaRekeyInterval,
		ipSecChildSaRekeyInterval: DefaultChildSaRekeyInterval,
		localCluster:              localCluster,
		localEndpoint:             localEndpoint,
		localSubnets:              localSubnets,
		secretKey:                 ipSecSpec.PSK,
		debug:                     ipSecSpec.Debug,
		logFile:                   ipSecSpec.LogFile,
	}, nil
}

func (i *engine) StartEngine() error {
	klog.Infof("Starting IPSec Engine (Charon)")
	ifi, err := util.GetDefaultGatewayInterface()
	if err != nil {
		return err
	}

	klog.V(8).Infof("Device of default gateway interface was %s", ifi.Name)
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error while initializing iptables: %v", err)
	}

	klog.V(6).Infof("Installing/ensuring the SUBMARINER-POSTROUTING and SUBMARINER-FORWARD chains")
	if err = ipt.NewChain("nat", "SUBMARINER-POSTROUTING"); err != nil {
		klog.Errorf("Unable to create SUBMARINER-POSTROUTING chain in iptables: %v", err)
	}

	if err = ipt.NewChain("filter", "SUBMARINER-FORWARD"); err != nil {
		klog.Errorf("Unable to create SUBMARINER-FORWARD chain in iptables: %v", err)
	}

	forwardToSubPostroutingRuleSpec := []string{"-j", "SUBMARINER-POSTROUTING"}
	if err = ipt.AppendUnique("nat", "POSTROUTING", forwardToSubPostroutingRuleSpec...); err != nil {
		klog.Errorf("Unable to append iptables rule \"%s\": %v\n", strings.Join(forwardToSubPostroutingRuleSpec, " "), err)
	}

	forwardToSubForwardRuleSpec := []string{"-j", "SUBMARINER-FORWARD"}
	rules, err := ipt.List("filter", "FORWARD")
	if err != nil {
		return fmt.Errorf("error listing the rules in FORWARD chain: %v", err)
	}

	appendAt := len(rules) + 1
	insertAt := appendAt
	for i, rule := range rules {
		if rule == "-A FORWARD -j SUBMARINER-FORWARD" {
			insertAt = -1
			break
		} else if rule == "-A FORWARD -j REJECT --reject-with icmp-host-prohibited" {
			insertAt = i
			break
		}
	}

	if insertAt == appendAt {
		// Append the rule at the end of FORWARD Chain.
		if err = ipt.Append("filter", "FORWARD", forwardToSubForwardRuleSpec...); err != nil {
			klog.Errorf("Unable to append iptables rule \"%s\": %v\n", strings.Join(forwardToSubForwardRuleSpec, " "), err)
		}
	} else if insertAt > 0 {
		// Insert the rule in the FORWARD Chain.
		if err = ipt.Insert("filter", "FORWARD", insertAt, forwardToSubForwardRuleSpec...); err != nil {
			klog.Errorf("Unable to insert iptables rule \"%s\" at position %d: %v\n", strings.Join(forwardToSubForwardRuleSpec, " "),
				insertAt, err)
		}
	}

	if err = runCharon(i.debug, i.logFile); err != nil {
		return err
	}

	if err := i.loadConns(); err != nil {
		return fmt.Errorf("Failed to load connections from charon: %v", err)
	}
	return nil
}

func (i *engine) InstallCable(endpoint types.SubmarinerEndpoint) error {
	client, err := getClient()
	if err != nil {
		return err
	}
	defer client.Close()

	return i.installCableInternal(endpoint, client)
}

func (i *engine) installCableInternal(endpoint types.SubmarinerEndpoint, client *goStrongswanVici.ClientConn) error {
	if endpoint.Spec.ClusterID == i.localCluster.ID {
		klog.V(4).Infof("Not installing cable for local cluster")
		return nil
	}
	if reflect.DeepEqual(endpoint.Spec, i.localEndpoint.Spec) {
		klog.V(4).Infof("Not installing self")
		return nil
	}

	klog.V(2).Infof("Installing cable %s", endpoint.Spec.CableName)
	activeConnections, err := i.getActiveConns(endpoint.Spec.ClusterID, client)
	if err != nil {
		return err
	}
	for _, active := range activeConnections {
		klog.V(6).Infof("Analyzing currently active connection: %s", active)
		if active == endpoint.Spec.CableName {
			klog.V(6).Infof("Cable %s is already installed, not installing twice", active)
			return nil
		}
		if util.GetClusterIDFromCableName(active) == endpoint.Spec.ClusterID {
			return fmt.Errorf("error while installing cable %s, already found a pre-existing cable belonging to this cluster %s", active, endpoint.Spec.ClusterID)
		}
	}

	i.Lock()
	defer i.Unlock()

	if err := i.loadSharedKey(endpoint, client); err != nil {
		return fmt.Errorf("Encountered issue while trying to load shared keys: %v", err)
	}

	var localEndpointIP, remoteEndpointIP string

	if endpoint.Spec.NATEnabled {
		localEndpointIP = i.localEndpoint.Spec.PublicIP.String()
		remoteEndpointIP = endpoint.Spec.PublicIP.String()
	} else {
		localEndpointIP = i.localEndpoint.Spec.PrivateIP.String()
		remoteEndpointIP = endpoint.Spec.PrivateIP.String()
	}

	var localTs, remoteTs, localAddr, remoteAddr []string
	localTs = append(localTs, fmt.Sprintf("%s/32", i.localEndpoint.Spec.PrivateIP.String()))
	localTs = append(localTs, i.localSubnets...)

	localAddr = append(localAddr, i.localEndpoint.Spec.PrivateIP.String())

	remoteTs = append(remoteTs, fmt.Sprintf("%s/32", endpoint.Spec.PrivateIP.String()))
	remoteTs = append(remoteTs, endpoint.Spec.Subnets...)

	remoteAddr = append(remoteAddr, remoteEndpointIP)
	// todo: make the ESP proposals configurable
	childSAConf := goStrongswanVici.ChildSAConf{
		Local_ts:      localTs,
		Remote_ts:     remoteTs,
		ESPProposals:  []string{"aes128gcm16-modp2048", "aes-modp2048"},
		StartAction:   "start",
		CloseAction:   "restart",
		Mode:          "tunnel",
		ReqID:         "0",
		RekeyTime:     i.ipSecChildSaRekeyInterval,
		InstallPolicy: "yes",
	}
	klog.V(6).Infof("Using ReplayWindowSize: %v", i.replayWindowSize)
	childSAConf.ReplayWindow = i.replayWindowSize
	authLConf := goStrongswanVici.AuthConf{
		ID:         localEndpointIP,
		AuthMethod: "psk",
	}
	authRConf := goStrongswanVici.AuthConf{
		ID:         remoteEndpointIP,
		AuthMethod: "psk",
	}
	ikeConf := goStrongswanVici.IKEConf{
		LocalAddrs:  localAddr,
		RemoteAddrs: remoteAddr,
		Proposals:   []string{"aes128gcm16-sha256-modp2048", "aes-sha1-modp2048"},
		Version:     "2",
		LocalAuth:   authLConf,
		RemoteAuth:  authRConf,
		RekeyTime:   i.ipSecIkeSaRekeyInterval,
		Encap:       "yes",
		Mobike:      "no",
	}
	ikeConf.Children = map[string]goStrongswanVici.ChildSAConf{
		"submariner-child-" + endpoint.Spec.CableName: childSAConf,
	}

	// Sometimes, the connection doesn't load, so try to load it multiple times.
	for i := 0; i < 6; i++ {
		err = client.LoadConn(&map[string]goStrongswanVici.IKEConf{
			endpoint.Spec.CableName: ikeConf,
		})
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("failed loading connection %s: %v", endpoint.Spec.CableName, err)
	}

	ifi, err := util.GetDefaultGatewayInterface()
	if err != nil {
		return err
	}
	klog.V(4).Infof("Device of default gateway interface was %s", ifi.Name)
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error while initializing iptables: %v", err)
	}

	addresses, err := ifi.Addrs()
	if err != nil {
		return err
	}

	for _, addr := range addresses {
		ipAddr, ipNet, err := net.ParseCIDR(addr.String())
		if err != nil {
			klog.Errorf("Error while parsing CIDR %s: %v", addr.String(), err)
			continue
		}

		if ipAddr.To4() != nil {
			for _, subnet := range endpoint.Spec.Subnets {
				ruleSpec := []string{"-s", ipNet.String(), "-d", subnet, "-i", ifi.Name, "-j", "ACCEPT"}
				klog.V(8).Infof("Installing iptables rule: %s", strings.Join(ruleSpec, " "))
				if err = ipt.AppendUnique("filter", "SUBMARINER-FORWARD", ruleSpec...); err != nil {
					klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
				}

				ruleSpec = []string{"-d", ipNet.String(), "-s", subnet, "-i", ifi.Name, "-j", "ACCEPT"}
				klog.V(8).Infof("Installing iptables rule: %v", ruleSpec)
				if err = ipt.AppendUnique("filter", "SUBMARINER-FORWARD", ruleSpec...); err != nil {
					klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
				}

				// -t nat -I POSTROUTING -s <local-network-cidr> -d <remote-cidr> -j SNAT --to-source <this-local-ip>
				ruleSpec = []string{"-s", ipNet.String(), "-d", subnet, "-j", "SNAT", "--to-source", ipAddr.String()}
				klog.V(8).Infof("Installing iptables rule: %s", strings.Join(ruleSpec, " "))
				if err = ipt.AppendUnique("nat", "SUBMARINER-POSTROUTING", ruleSpec...); err != nil {
					klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
				}
			}
		} else {
			klog.V(6).Infof("Skipping adding rule because IPv6 network %s found", ipNet.String())
		}
	}

	// MASQUERADE (on the GatewayNode) the incoming traffic from the remote cluster (i.e, remoteEndpointIP)
	// and destined to the local PODs (i.e., localSubnet) scheduled on the non-gateway node.
	// This will make the return traffic from the POD to go via the GatewayNode.
	for _, localSubnet := range i.localSubnets {
		ruleSpec := []string{"-s", remoteEndpointIP, "-d", localSubnet, "-j", "MASQUERADE"}
		klog.V(8).Infof("Installing iptables rule for MASQ incoming traffic: %v", ruleSpec)
		if err = ipt.AppendUnique("nat", "SUBMARINER-POSTROUTING", ruleSpec...); err != nil {
			klog.Errorf("error appending iptables MASQ rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	}

	klog.V(2).Infof("Loaded connection: %v", endpoint.Spec.CableName)

	return nil
}

func (i *engine) RemoveCable(cableID string) error {
	client, err := getClient()
	if err != nil {
		return err

	}
	defer client.Close()

	return i.removeCableInternal(cableID, client)
}

func (i *engine) removeCableInternal(cableID string, client *goStrongswanVici.ClientConn) error {
	i.Lock()
	defer i.Unlock()

	klog.Infof("Unloading connection %s", cableID)
	err := client.UnloadConn(&goStrongswanVici.UnloadConnRequest{
		Name: cableID,
	})
	if err != nil {
		return fmt.Errorf("Error when unloading connection %s : %v", cableID, err)
	}

	connections, err := client.ListConns("")
	if err != nil {
		klog.Errorf("Error while retrieving connections active after delete")
	} else {
		for _, conn := range connections {
			klog.V(6).Infof("Found connection %v", conn)
		}
	}

	saDeleted := false
	count := 0
	for {
		if saDeleted {
			break
		}
		if count > 2 {
			klog.Infof("Waited for connection terminate for 2 iterations, triggering a force delete of the IKE")
			err = client.Terminate(&goStrongswanVici.TerminateRequest{
				Ike:   cableID,
				Force: "yes",
			})
			if err != nil {
				klog.Errorf("error when terminating ike connection %s : %v", cableID, err)
			}
		}
		sas, err := client.ListSas("", "")
		if err != nil {
			klog.Errorf("error while retrieving sas active after delete")
		} else {
			found := false
			for _, samap := range sas {
				klog.V(6).Infof("Found SA %v", samap)
				sa, stillExists := samap[cableID]
				if stillExists && (sa.State == "DELETING" || sa.State == "CONNECTING") {
					found = true
					break
				} else if sa.State == "ESTABLISHED" {
					// what should we do here in this case? it could be perfectly legitimate that the connection was re-established,
					// i.e. datastore expired a good connection that was dead temporarily... in this case the sa shows up the way we want
					// if a true failover happens there should never be an established connection, but perhaps we should force delete
					// the connection anyway
					klog.V(4).Infof("It appears the peer became healthy again, and this connection was established")
					saDeleted = true
					break
				}
			}
			if found {
				klog.V(6).Infof("SA is still in deleting state; waiting 5 seconds before looking again")
				count++
				time.Sleep(5 * time.Second)
			} else {
				saDeleted = true
			}
		}
	}
	klog.Infof("Removed connection %s", cableID)
	return nil
}

func (i *engine) getActiveConns(clusterID string, client *goStrongswanVici.ClientConn) ([]string, error) {
	i.Lock()
	defer i.Unlock()
	var connections []string
	prefix := fmt.Sprintf("submariner-cable-%s-", clusterID)

	conns, err := client.ListConns("")
	if err != nil {
		return nil, err
	}

	for _, conn := range conns {
		for k := range conn {
			if strings.HasPrefix(k, prefix) {
				klog.V(4).Infof("Found existing connection: %s", k)
				connections = append(connections, k)
			}
		}
	}
	return connections, nil
}

func (i *engine) loadSharedKey(endpoint types.SubmarinerEndpoint, client *goStrongswanVici.ClientConn) error {
	klog.Infof("Loading shared key for endpoint")
	var identities []string
	var publicIP, privateIP string
	privateIP = endpoint.Spec.PrivateIP.String()
	identities = append(identities, privateIP)
	if endpoint.Spec.NATEnabled {
		publicIP = endpoint.Spec.PublicIP.String()
		if publicIP != privateIP {
			identities = append(identities, publicIP)
		}
	}
	sharedKey := &goStrongswanVici.Key{
		Typ:    "IKE",
		Data:   i.secretKey,
		Owners: identities,
	}

	err := client.LoadShared(sharedKey)
	if err != nil {
		klog.Infof("Failed to load pre-shared key for %s: %v", privateIP, err)
		if endpoint.Spec.NATEnabled {
			klog.Infof("Failed to load pre-shared key for %s: %v", publicIP, err)
		}
		return err
	}
	return nil
}

func runCharon(debug bool, logFile string) error {
	klog.Infof("Starting Charon")
	// Ignore error
	os.Remove("/var/run/charon.vici")

	args := []string{}
	for _, i := range strings.Split("dmn|mgr|ike|chd|cfg|knl|net|asn|tnc|imc|imv|pts|tls|esp|lib", "|") {
		args = append(args, "--debug-"+i)
		if debug {
			args = append(args, "3")
		} else {
			args = append(args, "1")
		}
	}

	cmd := exec.Command("charon", args...)
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
		return fmt.Errorf("error starting the charon process wih args %v: %v", args, err)
	}

	go func() {
		defer outputFile.Close()
		klog.Fatalf("charon exited: %v", cmd.Wait())
	}()

	return nil
}

func (i *engine) loadConns() error {
	i.Lock()
	defer i.Unlock()

	client, err := getClient()
	if err != nil {
		return err
	}
	defer client.Close()

	conns, err := client.ListConns("")
	if err != nil {
		return err
	}

	for _, conn := range conns {
		for k := range conn {
			if strings.HasPrefix(k, "submariner-conn-") {
				klog.Infof("Found existing connection: %s", k)
			}
		}
	}
	return nil
}

func getClient() (*goStrongswanVici.ClientConn, error) {
	var err error
	for i := 0; i < 3; i++ {
		var client *goStrongswanVici.ClientConn
		client, err = goStrongswanVici.NewClientConnFromDefaultSocket()
		if err == nil {
			return client, nil
		}

		if i > 0 {
			klog.Errorf("Failed to connect to charon: %v", err)
		}
		time.Sleep(1 * time.Second)
	}

	return nil, err
}
