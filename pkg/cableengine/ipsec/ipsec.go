package ipsec

import (
	"fmt"
	"github.com/bronze1man/goStrongswanVici"
	"github.com/kelseyhightower/envconfig"
	"github.com/rancher/submariner/pkg/types"
	"github.com/rancher/submariner/pkg/util"
	"io/ioutil"
	"k8s.io/klog"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"github.com/coreos/go-iptables/iptables"
)

const (
	pidFile  = "/var/run/charon.pid"

	// DefaultReplayWindowSize specifies the replay window size for charon
	DefaultReplayWindowSize = "1024"

	// DefaultIkeSaRekeyInterval specifies the default rekey interval for IKE_SA
	DefaultIkeSaRekeyInterval = "4h"

	// DefaultChildSaRekeyInterval specifies the default rekey interval for CHILD_SA
	DefaultChildSaRekeyInterval = "1h"
)

type Engine struct {
	sync.Mutex

	LocalSubnets []string
	LocalCluster types.SubmarinerCluster
	LocalEndpoint types.SubmarinerEndpoint
	SecretKey string
	ReplayWindowSize          string
	IPSecIkeSaRekeyInterval   string
	IPSecChildSaRekeyInterval string

	Debug bool
	LogFile string
}

type Specification struct {
	PSK     string
	Debug   bool
	LogFile string
}

func NewEngine(localSubnets []string, localCluster types.SubmarinerCluster, localEndpoint types.SubmarinerEndpoint) *Engine {

	ipSecSpec := Specification{}

	err := envconfig.Process("ce_ipsec", &ipSecSpec)
	if err != nil {
		klog.Fatal(err)
	}

	return &Engine{
		ReplayWindowSize:          DefaultReplayWindowSize,
		IPSecIkeSaRekeyInterval:   DefaultIkeSaRekeyInterval,
		IPSecChildSaRekeyInterval: DefaultChildSaRekeyInterval,
		LocalCluster: localCluster,
		LocalEndpoint: localEndpoint,
		LocalSubnets: localSubnets,
		SecretKey: ipSecSpec.PSK,
		Debug: ipSecSpec.Debug,
		LogFile: ipSecSpec.LogFile,
	}
}

func (i *Engine) StartEngine(ignition bool) error {
	if ignition {
		klog.Infof("Starting IPSec Engine (Charon)")
		ifi, err := util.GetDefaultGatewayInterface()
		if err != nil {
			klog.Fatal(err)
		}
		klog.V(8).Infof("Device of default gateway interface was %s", ifi.Name)
		ipt, err := iptables.New()
		if err != nil {
			klog.Fatalf("error while initializing iptables: %v", err)
		}

		klog.V(6).Infof("Installing/ensuring the SUBMARINER-POSTROUTING and SUBMARINER-FORWARD chains")
		ipt.NewChain("nat", "SUBMARINER-POSTROUTING")
		ipt.NewChain("filter", "SUBMARINER-FORWARD")
		forwardToSubPostroutingRuleSpec := []string{"-j", "SUBMARINER-POSTROUTING"}
		ipt.AppendUnique("nat", "POSTROUTING", forwardToSubPostroutingRuleSpec...)
		forwardToSubForwardRuleSpec := []string{"-j", "SUBMARINER-FORWARD"}
		rules, err := ipt.List("filter", "FORWARD")
		if err != nil {
			klog.Fatalf("error listing the rules in FORWARD chain: %v", err)
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
			ipt.Append("filter", "FORWARD", forwardToSubForwardRuleSpec...)
		} else if insertAt > 0 {
			// Insert the rule in the FORWARD Chain.
			ipt.Insert("filter", "FORWARD", insertAt, forwardToSubForwardRuleSpec...)
		}

		go runCharon(i.Debug, i.LogFile)
	}

	if err := i.loadConns(); err != nil {
		klog.Fatalf("Failed to load connections from charon: %v", err)
		return err
	}
	return nil
}

func (i *Engine) ReloadEngine() error {
	// to be implemented
	return nil

}
func (i *Engine) StopEngine() error {
	// to be implemented
	return nil
}

func (i *Engine) SyncCables(clusterID string, endpoints []types.SubmarinerEndpoint) error {
	klog.V(2).Infof("Starting selective cable sync")

	client, err := getClient()
	if err != nil {
		return err
	}
	defer client.Close()

	activeConnections, err := i.getActiveConns(false, clusterID, client)
	if err != nil {
		return err
	}
	for _, active := range activeConnections {
		klog.V(6).Infof("Analyzing currently active connection: %s", active)
		delete := true
		for _, endpoint := range endpoints {
			connName := endpoint.Spec.CableName
			if active == connName {
				klog.V(6).Infof("Active connection %s was found in the list of endpoints, not stopping it", connName)
				delete = false
			}
		}
		if delete {
			klog.Infof("Triggering remove cable of connection %s", active)
			i.removeCableInternal(active, client)
		}
	}

	for _, endpoint := range endpoints {
		connInstalled := false
		connName := endpoint.Spec.CableName
		for _, cname := range activeConnections {
			if connName == cname {
				klog.Infof("Cable with name: %s was already installed", connName)
				connInstalled = true
			}
		}
		if !connInstalled {
			klog.Infof("Marking cable %s to be installed", endpoint.Spec.CableName)
			i.installCableInternal(endpoint, client)
		}
	}

	return nil
}

func (i *Engine) InstallCable(endpoint types.SubmarinerEndpoint) error {
	client, err := getClient()
	if err != nil {
		return err
	}
	defer client.Close()

	return i.installCableInternal(endpoint, client);
}

func (i *Engine) installCableInternal(endpoint types.SubmarinerEndpoint, client *goStrongswanVici.ClientConn) error {
	if endpoint.Spec.ClusterID == i.LocalCluster.ID {
		klog.V(4).Infof("Not installing cable for local cluster")
		return nil
	}
	if reflect.DeepEqual(endpoint.Spec, i.LocalEndpoint.Spec) {
		klog.V(4).Infof("Not installing self")
		return nil
	}

	klog.V(2).Infof("Installing cable %s", endpoint.Spec.CableName)
	activeConnections, err := i.getActiveConns(false, endpoint.Spec.ClusterID, client)
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
		klog.Errorf("Encountered issue while trying to load shared keys")
		return err
	}

	var localEndpointIP, remoteEndpointIP string

	if endpoint.Spec.NATEnabled {
		localEndpointIP = i.LocalEndpoint.Spec.PublicIP.String()
		remoteEndpointIP = endpoint.Spec.PublicIP.String()
	} else {
		localEndpointIP = i.LocalEndpoint.Spec.PrivateIP.String()
		remoteEndpointIP = endpoint.Spec.PrivateIP.String()
	}

	var localTs, remoteTs, localAddr, remoteAddr []string
	localTs = append(localTs, fmt.Sprintf("%s/32", i.LocalEndpoint.Spec.PrivateIP.String()))
	localTs = append(localTs, i.LocalSubnets...)

	localAddr = append(localAddr, i.LocalEndpoint.Spec.PrivateIP.String())

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
		RekeyTime:     i.IPSecChildSaRekeyInterval,
		InstallPolicy: "yes",
	}
	klog.V(6).Infof("Using ReplayWindowSize: %v", i.ReplayWindowSize)
	childSAConf.ReplayWindow = i.ReplayWindowSize
	authLConf := goStrongswanVici.AuthConf{
		ID: localEndpointIP,
		AuthMethod: "psk",
	}
	authRConf := goStrongswanVici.AuthConf{
		ID: remoteEndpointIP,
		AuthMethod: "psk",
	}
	ikeConf := goStrongswanVici.IKEConf{
		LocalAddrs:  localAddr,
		RemoteAddrs: remoteAddr,
		Proposals:   []string{"aes128gcm16-sha256-modp2048", "aes-sha1-modp2048"},
		Version:     "2",
		LocalAuth:   authLConf,
		RemoteAuth:  authRConf,
		RekeyTime:	 i.IPSecIkeSaRekeyInterval,
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
		klog.Errorf("failed loading connection %s: %v", endpoint.Spec.CableName, err)
		return err
	}

	ifi, err := util.GetDefaultGatewayInterface()
	if err != nil {
		klog.Fatal(err)
	}
	klog.V(4).Infof("Device of default gateway interface was %s", ifi.Name)
	ipt, err := iptables.New()
	if err != nil {
		klog.Fatalf("error while initializing iptables: %v", err)
	}

	addresses, err := ifi.Addrs()
	if err != nil {
		klog.Fatal(err)
	}

	for _, addr := range addresses {
		ipAddr, ipNet, err := net.ParseCIDR(addr.String())
		if err != nil {
			klog.Errorf("error while parsing address %v", err)
		}
		if ipAddr.To4() != nil {
			for _, subnet := range endpoint.Spec.Subnets {
				ruleSpec := []string{"-s", ipNet.String(), "-d", subnet, "-i", ifi.Name, "-j", "ACCEPT"}
				klog.V(8).Infof("Installing iptables rule: %s", strings.Join(ruleSpec, " "))
				err = ipt.AppendUnique("filter", "SUBMARINER-FORWARD", ruleSpec...)
				if err != nil {
					klog.Errorf("error while installing iptables rule: %v", err)
				}

				ruleSpec = []string{"-d", ipNet.String(), "-s", subnet, "-i", ifi.Name, "-j", "ACCEPT"}
				klog.V(8).Infof("Installing iptables rule: %s", strings.Join(ruleSpec, " "))
				err = ipt.AppendUnique("filter", "SUBMARINER-FORWARD", ruleSpec...)
				if err != nil {
					klog.Errorf("error while installing iptables rule: %v", err)
				}
				// -t nat -I POSTROUTING -s <local-network-cidr> -d <remote-cidr> -j SNAT --to-source <this-local-ip>
				ruleSpec = []string{"-s", ipNet.String(), "-d", subnet, "-j", "SNAT", "--to-source", ipAddr.String()}
				klog.V(8).Infof("Installing iptables rule: %s", strings.Join(ruleSpec, " "))
				err = ipt.AppendUnique("nat", "SUBMARINER-POSTROUTING", ruleSpec...)
				if err != nil {
					klog.Errorf("error while installing iptables rule: %v", err)
				}
			}
		} else {
			klog.V(6).Infof("Skipping adding rule because IPv6 network %s found", ipNet.String())
		}
	}

	// MASQUERADE (on the GatewayNode) the incoming traffic from the remote cluster (i.e, remoteEndpointIP)
	// and destined to the local PODs (i.e., localSubnet) scheduled on the non-gateway node.
	// This will make the return traffic from the POD to go via the GatewayNode.
	for _, localSubnet := range i.LocalSubnets {
		ruleSpec := []string{"-s", remoteEndpointIP, "-d", localSubnet, "-j", "MASQUERADE"}
		klog.V(8).Infof("Installing iptables rule for MASQ incoming traffic: %s", strings.Join(ruleSpec, " "))
		err = ipt.AppendUnique("nat", "SUBMARINER-POSTROUTING", ruleSpec...)
		if err != nil {
			klog.Errorf("error while installing iptables MASQ rule: %v", err)
		}
	}

	klog.V(2).Infof("Loaded connection: %v", endpoint.Spec.CableName)

	return nil
}

func (i *Engine) RemoveCable(cableID string) error {
	client, err := getClient()
	if err != nil {
		return err

	}
	defer client.Close()

	return i.removeCableInternal(cableID, client)
}

func (i *Engine) removeCableInternal(cableID string, client *goStrongswanVici.ClientConn) error {
	i.Lock()
	defer i.Unlock()

	/*
	err = client.Terminate(&goStrongswanVici.TerminateRequest{
		Child: "submariner-child-" + cableID,
	})
	if err != nil {
		klog.Errorf("Error when terminating child connection %s : %v", cableID, err)
	}

	err = client.Terminate(&goStrongswanVici.TerminateRequest{
		Ike: cableID,
	})
	if err != nil {
		klog.Errorf("Error when terminating ike connection %s : %v", cableID, err)
	} */
	klog.Infof("Unloading connection %s", cableID)
	err := client.UnloadConn(&goStrongswanVici.UnloadConnRequest{
		Name: cableID,
	})
	if err != nil {
		klog.Errorf("Error when unloading connection %s : %v", cableID, err)
		return err
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
				if stillExists && ( sa.State == "DELETING" || sa.State == "CONNECTING" ){
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

func (i *Engine) PrintConns() {
	client, _ := getClient()
	defer client.Close()
	connList, err := client.ListConns("")
	if err != nil {
		klog.Errorf("error list-conns: %v \n", err)
	}

	for _, connection := range connList {
		klog.Infof("connection map: %v", connection)
	}
}

func (i *Engine) getActiveConns(getAll bool, clusterID string, client *goStrongswanVici.ClientConn) ([]string, error) {
	i.Lock()
	defer i.Unlock()
	var connections []string
	var prefix string
	if getAll {
		prefix = "submariner-cable-"
	} else {
		prefix = fmt.Sprintf("submariner-cable-%s-", clusterID)
	}

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

func (i *Engine) loadSharedKey(endpoint types.SubmarinerEndpoint, client *goStrongswanVici.ClientConn) error {
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
		Data:   i.SecretKey,
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

func runCharon(debug bool, logFile string) {
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

	if logFile != "" {
		output, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			klog.Fatalf("Failed to log to file %s: %v", logFile, err)
		}
		defer output.Close()
		cmd.Stdout = output
		cmd.Stderr = output
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}

	cmd.Start()

	klog.Fatalf("charon exited: %v", cmd.Wait())
}

func (i *Engine) killCharon(pid string) {
	pidNum, err := strconv.Atoi(pid)
	if err == nil {
		err = syscall.Kill(pidNum, syscall.SIGKILL)
	}

	if err != nil {
		klog.Errorf("Can't kill %s: %v", pid, err)
	}
}

func (i *Engine) loadConns() error {
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

func (i *Engine) monitorCharon() {
	pid := ""
	for {
		newPidBytes, err := ioutil.ReadFile(pidFile)
		if err != nil {
			klog.Fatalf("Failed to read %s", pidFile)
		}
		newPid := strings.TrimSpace(string(newPidBytes))
		if pid == "" {
			pid = newPid
			klog.Infof("Charon running PID: %s", pid)
		} else if pid != newPid {
			klog.Fatalf("Charon restarted, old PID: %s, new PID: %s", pid, newPid)
		} else {
			i.Lock()
			if err := Test(); err != nil {
				klog.Errorf("Killing charon due to: %v", err)
				i.killCharon(pid)
			}
			i.Unlock()
		}
		time.Sleep(2 * time.Second)
	}
}

func Test() error {
	client, err := getClient()
	if err != nil {
		return err
	}
	defer client.Close()

	if _, err := client.ListConns(""); err != nil {
		return err
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