package nbctl

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/util"
)

type NbCtl struct {
	ClientKey        string
	ClientCert       string
	CA               string
	ConnectionString string
}

func New(db, clientkey, clientcert, ca string) *NbCtl {
	return &NbCtl{
		ConnectionString: db,
		ClientKey:        clientkey,
		ClientCert:       clientcert,
		CA:               ca,
	}
}

func (n *NbCtl) nbctl(parameters ...string) (output string, err error) {
	allParameters := []string{
		fmt.Sprintf("--db=%s", n.ConnectionString),
		"-c", n.ClientCert,
		"-p", n.ClientKey,
		"-C", n.CA,
	}

	allParameters = append(allParameters, parameters...)

	cmd := exec.Command("/usr/bin/ovn-nbctl", allParameters...)

	klog.V(log.DEBUG).Infof("ovn-nbctl %v", allParameters)

	out, err := cmd.CombinedOutput()

	strOut := string(out)

	if err != nil {
		klog.Errorf("Error running ovn-nbctl %+v, output:\n%s", err, strOut)
		return strOut, err
	}

	return strOut, err
}

func (n *NbCtl) SetGatewayChassis(lrp, chassis string, prio int) error {
	_, err := n.nbctl("lrp-set-gateway-chassis", lrp, chassis, strconv.Itoa(prio))
	return err
}

func (n *NbCtl) DelGatewayChassis(lrp, chassis string, prio int) error {
	_, err := n.nbctl("lrp-del-gateway-chassis", lrp, chassis, strconv.Itoa(prio))
	return err
}

type GatewayChassis struct {
}

func (n *NbCtl) GetGatewayChassis(lrp, chassis string) (string, error) {
	output, err := n.nbctl("lrp-get-gateway-chassis", lrp, chassis)
	return output, err
}

func (n *NbCtl) LrPolicyAdd(logicalRouter string, prio int, filter string, actions ...string) error {
	allParameters := []string{"lr-policy-add", logicalRouter, strconv.Itoa(prio), filter}
	allParameters = append(allParameters, actions...)
	_, err := n.nbctl(allParameters...)

	return err
}
func (n *NbCtl) LrPolicyDel(logicalRouter string, prio int, filter string) error {
	_, err := n.nbctl("lr-policy-del", logicalRouter, strconv.Itoa(prio), filter)
	return err
}

func (n *NbCtl) LrPolicyGetSubnets(logicalRouter, rerouteIp string) (*util.StringSet, error) {
	output, err := n.nbctl("lr-policy-list", logicalRouter)
	if err != nil {
		return nil, errors.Wrapf(err, "Error getting existing routing policies for router %q", logicalRouter)
	}

	return parseLrPolicyGetOutput(output, rerouteIp), nil
}

func parseLrPolicyGetOutput(output, rerouteIp string) *util.StringSet {
	// Example output:
	// $ ovn-nbctl lr-policy-list submariner_router
	// Routing Policies
	//        10                                 ip.src==1.1.1.1/32         reroute              169.254.34.1
	//        10                                 ip.src==1.1.1.2/32         reroute              169.254.34.1
	//
	subnets := util.NewStringSet()
	r := regexp.MustCompile("ip4\\.dst == ([0-9\\.]+/[0-9]+)[\\s\\t]+reroute[\\s\\t]+" + rerouteIp)
	for _, match := range r.FindAllStringSubmatch(output, -1) {
		if len(match) == 2 {
			subnets.Add(match[1])
		}
	}

	return subnets
}
