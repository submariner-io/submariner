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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ovsCommandTimeout = 15
	ifexistsArg       = "--if-exists"
)

var logger = log.Logger{Logger: logf.Log.WithName("ovs-vsctl")}

func vsctlCmd(parameters ...string) error {
	allParameters := make([]string, 1, 1+len(parameters))
	allParameters[0] = fmt.Sprintf("--timeout=%d", ovsCommandTimeout)
	allParameters = append(allParameters, parameters...)

	cmd := exec.Command("/usr/bin/ovs-vsctl", allParameters...)

	logger.V(log.TRACE).Infof("Running ovs-vsctl %v", allParameters)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()

	stdout := string(out)

	if err != nil {
		logger.Errorf(err, "Error running ovs-vsctl, stdout:\n%s\nstderr:\n%s", stdout, stderr.String())

		return errors.Wrap(err, "error running ovs-vsctl")
	}

	return nil
}

func DelBridge(bridgeName string) error {
	return vsctlCmd(ifexistsArg, "del-br", bridgeName)
}

func DelInternalPort(bridgeName, portName string) error {
	return vsctlCmd(ifexistsArg, "del-port", bridgeName, portName)
}
