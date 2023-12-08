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

package util

import (
	"fmt"
	"os"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("InterfaceWatcher")}

type InterfaceWatcher struct {
	InterfaceName string
	Done          chan struct{}
}

// NewInterfaceWatcher creates a new InterfaceWatcher for the given interface.
func NewInterfaceWatcher(interfaceName string) (*InterfaceWatcher, error) {
	return &InterfaceWatcher{
		InterfaceName: interfaceName,
		Done:          make(chan struct{}),
	}, nil
}

// Monitor periodically checks the rp_filter setting for the interface.
func (iw *InterfaceWatcher) Monitor() {
	go func() {
		for {
			select {
			case <-iw.Done:
				// Done signal received
				return
			default:
				// Check and update the rp_filter setting
				if err := iw.checkAndUpdateRpFilter(); err != nil {
					logger.Errorf(err, "error checking/updating rp_filter setting for %q\n", iw.InterfaceName)
				}

				// Sleep for a specific interval before the next check
				time.Sleep(5 * time.Second)
			}
		}
	}()
}

func (iw *InterfaceWatcher) checkAndUpdateRpFilter() error {
	// Get the current rp_filter setting for the interface
	currentRpFilter, err := iw.getCurrentRpFilterSetting()
	if err != nil {
		return err
	}

	logger.Infof("Current rp_filter setting for %s: %d\n", iw.InterfaceName, currentRpFilter)

	// If the current setting is not 2, update it to 2
	if currentRpFilter != 2 {
		if err := iw.setRpFilterSetting(2); err != nil {
			return err
		}

		logger.Infof("The rp_filter setting for %s updated to 2\n", iw.InterfaceName)
	}

	return nil
}

func (iw *InterfaceWatcher) getCurrentRpFilterSetting() (int, error) {
	// Read the content of the rp_filter file directly
	netPath := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", iw.InterfaceName)

	content, err := ReadFile(netPath)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to read rp_filter setting for %q", iw.InterfaceName)
	}

	// Parse the rp_filter value
	var rpFilterValue int

	_, err = fmt.Sscanf(string(content), "%d", &rpFilterValue)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to parse rp_filter setting for %q", iw.InterfaceName)
	}

	return rpFilterValue, nil
}

func (iw *InterfaceWatcher) setRpFilterSetting(value int) error {
	// Write the value to the rp_filter file directly
	netPath := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", iw.InterfaceName)

	err := WriteFile(netPath, fmt.Sprintf("%d", value))
	if err != nil {
		return errors.Wrapf(err, "failed to set rp_filter setting for %s", iw.InterfaceName)
	}

	return nil
}

func ReadFile(filename string) ([]byte, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read file %s", filename)
	}

	return content, nil
}

func WriteFile(filename, content string) error {
	err := os.WriteFile(filename, []byte(content), 0o600)
	if err != nil {
		return errors.Wrapf(err, "failed to write to file %s", filename)
	}

	return nil
}
