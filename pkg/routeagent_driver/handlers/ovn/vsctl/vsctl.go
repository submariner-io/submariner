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

package vsctl

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const ovsCommandTimeout = 15

var logger = log.Logger{Logger: logf.Log.WithName("ovs-vsctl")}

func vsctlCmd(parameters ...string) (output string, err error) {
	allParameters := []string{fmt.Sprintf("--timeout=%d", ovsCommandTimeout)}
	allParameters = append(allParameters, parameters...)

	cmd := exec.Command("/usr/bin/ovs-vsctl", allParameters...)

	logger.V(log.TRACE).Infof("Running ovs-vsctl %v", allParameters)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()

	stdout := string(out)

	if err != nil {
		stdErrOut := stderr.String()
		logger.Errorf(err, "Error running ovs-vsctl, output:\n%s", stdErrOut)

		return stdErrOut, errors.Wrap(err, "error running ovs-vsctl")
	}

	return stdout, nil
}

func AddBridge(bridgeName string) error {
	_, err := vsctlCmd("--may-exist", "add-br", bridgeName)

	return err
}

func DelBridge(bridgeName string) error {
	_, err := vsctlCmd("del-br", bridgeName)

	return err
}

func AddInternalPort(bridgeName, portName, macAddress string, mtu int) error {
	_, err := vsctlCmd("--may-exist", "add-port", bridgeName, portName, "--",
		"set", "interface", portName, "type=internal", "mtu_request="+fmt.Sprintf("%d", mtu),
		fmt.Sprintf("mac=%s", strings.ReplaceAll(macAddress, ":", "\\:")))

	return err
}

func DelInternalPort(bridgeName, portName string) error {
	_, err := vsctlCmd("--if-exists", "del-port", bridgeName, portName)

	return err
}

func AddOVNBridgeMapping(netName, bridgeName string) error {
	stdout, err := vsctlCmd("--if-exists", "get", "Open_vSwitch", ".",
		"external_ids:ovn-bridge-mappings")
	if err != nil {
		return errors.Wrap(err, "failed to get ovn-bridge-mappings")
	}

	// cleanup the ovs-vsctl output, and remove quotes
	bridgeMappings := strings.Trim(strings.TrimSpace(stdout), "\"")

	locnetMapping := fmt.Sprintf("%s:%s", netName, bridgeName)
	if strings.Contains(bridgeMappings, locnetMapping) {
		return nil // it was already set
	}

	if len(bridgeMappings) > 0 {
		bridgeMappings = bridgeMappings + "," + locnetMapping
	} else {
		bridgeMappings = locnetMapping
	}

	if _, err = vsctlCmd("set", "Open_vSwitch", ".",
		fmt.Sprintf("external_ids:ovn-bridge-mappings=%s", bridgeMappings)); err != nil {
		return errors.Wrap(err, "failed to set ovn-bridge-mappings")
	}

	return nil
}
