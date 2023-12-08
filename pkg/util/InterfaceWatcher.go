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

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type InterfaceWatcher struct {
	InterfaceName string
	Watcher       *fsnotify.Watcher
	Done          chan struct{}
}

var logger = log.Logger{Logger: logf.Log.WithName("InterfaceWatcher")}

// NewInterfaceWatcher creates a new InterfaceWatcher for the given interface.
func NewInterfaceWatcher(interfaceName string) (*InterfaceWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create watcher for %s", interfaceName)
	}

	// Add the file to the watcher
	netPath := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", interfaceName)

	err = watcher.Add(netPath)
	if err != nil {
		watcher.Close()
		return nil, errors.Wrapf(err, "failed to add file to watcher for %s", interfaceName)
	}

	return &InterfaceWatcher{
		InterfaceName: interfaceName,
		Watcher:       watcher,
		Done:          make(chan struct{}),
	}, nil
}

// Monitor starts monitoring the rp_filter setting for the interface.
func (iw *InterfaceWatcher) Monitor() {
	logger.Infof("Starting RP filer monitor for %q", iw.InterfaceName)
	defer close(iw.Done)

	go func() {
		for {
			select {
			case event, ok := <-iw.Watcher.Events:
				if !ok {
					return
				}

				if event.Op&fsnotify.Write == fsnotify.Write {
					logger.Infof("rp_filter setting has changed for %s\n", iw.InterfaceName)

					// Check the current rp_filter setting
					rpFilterValue, err := iw.GetCurrentRpFilterSetting()
					if err != nil {
						logger.Errorf(err, "Error getting rp_filter setting for %s\n", iw.InterfaceName)
						continue
					}

					logger.Infof("Current rp_filter setting for %s: %d\n", iw.InterfaceName, rpFilterValue)

					// Reset to 2 if the value is not
					if rpFilterValue != 2 {
						err := iw.SetRpFilterSetting(2)
						if err != nil {
							logger.Errorf(err, "Error setting rp_filter to 2 for %sc\n", iw.InterfaceName)
						} else {
							logger.Infof("rp_filter set to 2 for %s\n", iw.InterfaceName)
						}
					}
				}

			case err, ok := <-iw.Watcher.Errors:
				if !ok {
					return
				}

				logger.Errorf(err, "Error:")

			case <-iw.Done:
				return
			}
		}
	}()
}

func (iw *InterfaceWatcher) GetCurrentRpFilterSetting() (int, error) {
	// Read the content of the rp_filter file directly
	netPath := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", iw.InterfaceName)

	content, err := ReadFile(netPath)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to read rp_filter setting for %s", iw.InterfaceName)
	}

	// Parse the rp_filter value
	var rpFilterValue int

	_, err = fmt.Sscanf(string(content), "%d", &rpFilterValue)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to parse rp_filter setting for %s", iw.InterfaceName)
	}

	return rpFilterValue, nil
}

func (iw *InterfaceWatcher) SetRpFilterSetting(value int) error {
	// Write the value to the rp_filter file directly
	netPath := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", iw.InterfaceName)

	err := WriteFile(netPath, fmt.Sprintf("%d", value))
	if err != nil {
		return errors.Wrapf(err, "failed to set rp_filter setting for %s", iw.InterfaceName)
	}

	return nil
}

func WriteFile(filename, content string) error {
	err := os.WriteFile(filename, []byte(content), 0o600)
	if err != nil {
		return errors.Wrapf(err, "failed to write to file %s", filename)
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
