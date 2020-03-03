package ipsec

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/template"
	"time"

	"k8s.io/klog"

	"github.com/bronze1man/goStrongswanVici"
	"github.com/kelseyhightower/envconfig"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/log"
	"github.com/submariner-io/submariner/pkg/types"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

const (
	// DefaultReplayWindowSize specifies the replay window size for charon
	DefaultReplayWindowSize = "1024"

	// DefaultIkeSaRekeyInterval specifies the default rekey interval for IKE_SA
	DefaultIkeSaRekeyInterval = "4h"

	// DefaultChildSaRekeyInterval specifies the default rekey interval for CHILD_SA
	DefaultChildSaRekeyInterval = "1h"

	// strongswanCharonConfigFilePAth points to the config file charon will use at start
	strongswanCharonConfigFilePath = "/etc/strongswan/strongswan.d/charon.conf"

	// charonViciSocket points to the vici socket exposed by charon
	charonViciSocket = "/var/run/charon.vici"

	cableDriverName = "strongswan"
)

func init() {
	cable.SetDefautCableDriver(cableDriverName)
	cable.AddDriver(cableDriverName, NewStrongSwan)
}

type strongSwan struct {
	localSubnets              []string
	localEndpoint             types.SubmarinerEndpoint
	secretKey                 string
	replayWindowSize          string
	ipSecIkeSaRekeyInterval   string
	ipSecChildSaRekeyInterval string
	ipSecIKEPort              string
	ipSecNATTPort             string

	debug   bool
	logFile string
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

func NewStrongSwan(localSubnets []string, localEndpoint types.SubmarinerEndpoint) (cable.Driver, error) {
	ipSecSpec := specification{}

	err := envconfig.Process(ipsecSpecEnvVarPrefix, &ipSecSpec)
	if err != nil {
		return nil, fmt.Errorf("error processing environment config for ce_ipsec: %v", err)
	}

	return &strongSwan{
		replayWindowSize:          DefaultReplayWindowSize,
		ipSecIkeSaRekeyInterval:   DefaultIkeSaRekeyInterval,
		ipSecChildSaRekeyInterval: DefaultChildSaRekeyInterval,
		ipSecIKEPort:              ipSecSpec.IKEPort,
		ipSecNATTPort:             ipSecSpec.NATTPort,
		localEndpoint:             localEndpoint,
		localSubnets:              localSubnets,
		secretKey:                 ipSecSpec.PSK,
		debug:                     ipSecSpec.Debug,
		logFile:                   ipSecSpec.LogFile,
	}, nil
}

func (i *strongSwan) Init() error {
	klog.Info("Initializing StrongSwan IPSec driver")

	if err := i.runCharon(); err != nil {
		return err
	}

	if err := i.loadConns(); err != nil {
		return fmt.Errorf("failed to load connections from charon: %v", err)
	}
	return nil
}

func (i *strongSwan) ConnectToEndpoint(endpoint types.SubmarinerEndpoint) (string, error) {
	client, err := getClient()
	if err != nil {
		return "", err
	}
	defer client.Close()

	if err := i.loadSharedKey(endpoint, client); err != nil {
		return "", fmt.Errorf("error loading shared keys: %v", err)
	}

	var localEndpointIP, remoteEndpointIP string

	if endpoint.Spec.NATEnabled {
		localEndpointIP = i.localEndpoint.Spec.PublicIP
		remoteEndpointIP = endpoint.Spec.PublicIP
	} else {
		localEndpointIP = i.localEndpoint.Spec.PrivateIP
		remoteEndpointIP = endpoint.Spec.PrivateIP
	}

	var localTs, remoteTs, localAddr, remoteAddr []string
	localTs = append(localTs, fmt.Sprintf("%s/32", i.localEndpoint.Spec.PrivateIP))
	localTs = append(localTs, i.localSubnets...)

	localAddr = append(localAddr, i.localEndpoint.Spec.PrivateIP)

	remoteTs = append(remoteTs, fmt.Sprintf("%s/32", endpoint.Spec.PrivateIP))
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

	klog.V(log.TRACE).Infof("Using ReplayWindowSize: %v", i.replayWindowSize)
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

	// We point to the remote port that has proven to work by trial and error
	// with strongswan over non-standard UDP ports
	if i.ipSecNATTPort != defaultNATTPort {
		ikeConf.RemotePort = i.ipSecNATTPort
	} else if i.ipSecIKEPort != defaultIKEPort {
		ikeConf.RemotePort = i.ipSecIKEPort
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
		return "", fmt.Errorf("error loading connection %q with config %#v into charon: %v", endpoint.Spec.CableName, ikeConf, err)
	}

	return remoteEndpointIP, nil
}

func (i *strongSwan) DisconnectFromEndpoint(endpoint types.SubmarinerEndpoint) error {
	client, err := getClient()
	if err != nil {
		return err

	}
	defer client.Close()

	cableID := endpoint.Spec.CableName
	err = client.UnloadConn(&goStrongswanVici.UnloadConnRequest{
		Name: cableID,
	})

	if err != nil {
		return fmt.Errorf("error unloading connection %q from charon: %v", cableID, err)
	}

	connections, err := client.ListConns("")
	if err != nil {
		klog.Errorf("Error while retrieving active connections after delete: %v", err)
	} else {
		for _, conn := range connections {
			klog.V(log.TRACE).Infof("Found connection %#v", conn)
		}
	}

	saDeleted := false
	count := 0
	for {
		if saDeleted {
			break
		}

		if count > 2 {
			klog.Infof("Waited for connection termination for 2 iterations - triggering a force delete of the IKE")
			err = client.Terminate(&goStrongswanVici.TerminateRequest{
				Ike:   cableID,
				Force: "yes",
			})

			if err != nil {
				klog.Errorf("Error terminating connection %q: %v", cableID, err)
			}
		}

		sas, err := client.ListSas("", "")
		if err != nil {
			klog.Errorf("Error while retrieving active IKE_SAs after delete: %v", err)
		} else {
			found := false
			for _, samap := range sas {
				klog.V(log.TRACE).Infof("Found SA %v", samap)
				sa, stillExists := samap[cableID]
				if stillExists && (sa.State == "DELETING" || sa.State == "CONNECTING") {
					found = true
					break
				} else if sa.State == "ESTABLISHED" {
					// what should we do here in this case? it could be perfectly legitimate that the connection was re-established,
					// i.e. datastore expired a good connection that was dead temporarily... in this case the sa shows up the way we want
					// if a true failover happens there should never be an established connection, but perhaps we should force delete
					// the connection anyway
					klog.V(log.DEBUG).Infof("It appears the peer became healthy again, and this connection was established")
					saDeleted = true
					break
				}
			}

			if found {
				klog.V(log.DEBUG).Infof("SA is still in deleting state - waiting 5 seconds before checking again")
				count++
				time.Sleep(5 * time.Second)
			} else {
				saDeleted = true
			}
		}
	}

	return nil
}

func (i *strongSwan) GetActiveConnections(clusterID string) ([]string, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var connections []string
	prefix := fmt.Sprintf("submariner-cable-%s-", clusterID)

	conns, err := client.ListConns("")
	if err != nil {
		return nil, err
	}

	for _, conn := range conns {
		for cableID := range conn {
			if !strings.HasPrefix(cableID, prefix) {
				continue
			}

			removed, err := i.removeStaleCable(client, cableID)
			if err != nil {
				return nil, fmt.Errorf("error removing stale cable %q: %v", cableID, err)
			}

			if !removed {
				klog.V(log.TRACE).Infof("Found existing connection %q", cableID)
				connections = append(connections, cableID)
			}
		}
	}
	return connections, nil
}

// removeStaleCable removes cables that have no active SAS and returns a
// boolean indicating removal of the cable. The status of the error is nil
// in case of no errors, otherwise the returns values are false and the
// error.
func (i *strongSwan) removeStaleCable(client *goStrongswanVici.ClientConn, cableID string) (bool, error) {
	sas, err := client.ListSas("", "")
	if err != nil {
		return false, fmt.Errorf("error while retrieving active IKE_SAs: %v", err)
	}

	for _, samap := range sas {
		klog.V(log.TRACE).Infof("Found SA %v", samap)
		sa, exists := samap[cableID]
		if exists && (sa.State == "ESTABLISHED" || sa.State == "CONNECTING") {
			// Cable with an active SA and thus not stale
			return false, nil
		}
	}

	klog.V(log.DEBUG).Infof("Removing stale cable: %s", cableID)

	// Creating this stub Endpoint is not ideal but to avoid a refactor
	// TODO(mpeterson): refactor code to make this not a requirement
	err = i.DisconnectFromEndpoint(types.SubmarinerEndpoint{
		Spec: subv1.EndpointSpec{
			CableName: cableID,
		},
	})

	if err != nil {
		return false, err
	}

	return true, nil
}

func (i *strongSwan) loadSharedKey(endpoint types.SubmarinerEndpoint, client *goStrongswanVici.ClientConn) error {
	var identities []string

	privateIP := endpoint.Spec.PrivateIP
	identities = append(identities, privateIP)
	if endpoint.Spec.NATEnabled {
		if endpoint.Spec.PublicIP != privateIP {
			identities = append(identities, endpoint.Spec.PublicIP)
		}
	}

	sharedKey := &goStrongswanVici.Key{
		Typ:    "IKE",
		Data:   i.secretKey,
		Owners: identities,
	}

	err := client.LoadShared(sharedKey)
	if err != nil {
		return fmt.Errorf("Error loading pre-shared key for %v: %v", identities, err)
	}
	return nil
}

// charonConfTemplate defines the configuration for strongswan IKE keying daemon,
// * port and port_nat_t define the IKE and IKE NATT UDP ports
// * make_before_break ensures dataplane connectivity while re-authenticating endpoints (check this)
// TODO : check what * ignore_acquire_ts means
const charonConfTemplate = `
	charon {
		port = {{.ipSecIKEPort}}
		port_nat_t = {{.ipSecNATTPort}}
		make_before_break = yes
		ignore_acquire_ts = yes
		plugins {
			vici {
				load = yes
				socket = unix://{{.charonViciSocket}}
			}
		}
	}
`

func (i *strongSwan) writeCharonConfig(path string) error {
	err := os.Remove(path)
	if err != nil {
		klog.Warningf("Error deleting charon config file %q: %v", path, err)
	}

	f, err := os.Create(path)

	if err != nil {
		return fmt.Errorf("error creating charon config file %q: %s", path, err)
	}

	if err = i.renderCharonConfigTemplate(f); err != nil {
		return err
	}

	if err = f.Close(); err != nil {
		return fmt.Errorf("error closing charon config file %q: %v", path, err)
	}

	return nil
}

func (i *strongSwan) renderCharonConfigTemplate(f io.Writer) error {
	t, err := template.New("charon.conf").Parse(charonConfTemplate)
	if err != nil {
		return fmt.Errorf("error creating template for charon.conf: %v", err)
	}

	err = t.Execute(f, map[string]string{
		"ipSecIKEPort":     i.ipSecIKEPort,
		"ipSecNATTPort":    i.ipSecNATTPort,
		"charonViciSocket": charonViciSocket})

	if err != nil {
		return fmt.Errorf("error rendering charon config file: %v", err)
	}
	return nil
}

func (i *strongSwan) runCharon() error {
	if err := i.writeCharonConfig(strongswanCharonConfigFilePath); err != nil {
		return fmt.Errorf("error writing strongswan charon config: %v", err)
	}

	klog.Infof("Starting charon")

	// Ignore error
	os.Remove(charonViciSocket)

	args := []string{}
	for _, idx := range strings.Split("dmn|mgr|ike|chd|cfg|knl|net|asn|tnc|imc|imv|pts|tls|esp|lib", "|") {
		args = append(args, "--debug-"+idx)
		if i.debug {
			args = append(args, "3")
		} else {
			args = append(args, "1")
		}
	}

	cmd := exec.Command("charon", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	var outputFile *os.File
	if i.logFile != "" {
		out, err := os.OpenFile(i.logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return fmt.Errorf("failed to open log file %q: %v", i.logFile, err)
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
		return fmt.Errorf("error starting the charon process with args %v: %v", args, err)
	}

	go func() {
		defer outputFile.Close()
		klog.Fatalf("charon exited: %v", cmd.Wait())
	}()

	return nil
}

func (i *strongSwan) loadConns() error {
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
				klog.Infof("Found existing charon connection %q", k)
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

		klog.Warningf("Failed to connect to charon - retrying: %v", err)
		time.Sleep(1 * time.Second)
	}

	return nil, err
}
