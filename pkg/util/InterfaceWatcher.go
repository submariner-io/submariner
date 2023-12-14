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
	"github.com/submariner-io/admiral/pkg/log"
	submnetlink "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/vishvananda/netlink"
	"os"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("InterfaceWatcher")}

// InterfaceWatcher represents the state for monitoring an interface
type InterfaceWatcher struct {
	InterfaceName string
	Done          chan struct{}
	netLink       submnetlink.Interface
}

// NewInterfaceWatcher creates a new InterfaceWatcher for the given interface
func NewInterfaceWatcher(interfaceName string) (*InterfaceWatcher, error) {
	return &InterfaceWatcher{
		InterfaceName: interfaceName,
		Done:          make(chan struct{}),
	}, nil
}

// Monitor starts monitoring the rp_filter setting for the interface
func (iw *InterfaceWatcher) Monitor() {
	linkCh := make(chan netlink.LinkUpdate)
	doneCh := make(chan struct{})

	go func() {
		err := netlink.LinkSubscribe(linkCh, doneCh)
		if err != nil {
			fmt.Printf("Error subscribing to link updates: %v\n", err)
		}
	}()

	go func() {
		for {
			logger.Infof("In for loop")
			select {
			case <-iw.Done:
				close(doneCh)
				close(linkCh)
				// Done signal received
				return
			case linkUpdate := <-linkCh:
				logger.Infof("Link update received")
				// Check for changes in the network interface
				if linkUpdate.Attrs().Name != iw.InterfaceName {
					continue
				}

				if err := iw.checkRpFilterSetting(); err != nil {
					logger.Infof("Error checking rp_filter setting for %s: %v\n", iw.InterfaceName, err)
				}
			}
		}
	}()
}

// checkRpFilterSetting checks for changes in the rp_filter setting for the interface
func (iw *InterfaceWatcher) checkRpFilterSetting() error {
	rpFilterValue, err := getRpFilterSetting(iw.InterfaceName)
	if err != nil {
		return err
	}

	logger.Infof("Current rp_filter setting for %s: %d\n", iw.InterfaceName, rpFilterValue)

	// Reset to 2 if the value is not 2
	if rpFilterValue != 2 {
		logger.Infof("Changing rp_filter to 2 for %s\n", iw.InterfaceName)
		err := setRpFilterSetting(iw.InterfaceName, 2)
		if err != nil {
			return err
		}
	}

	return nil
}

// getRpFilterSetting gets the rp_filter setting for the interface
func getRpFilterSetting(interfaceName string) (int, error) {
	filePath := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", interfaceName)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return 0, err
	}

	var rpFilterValue int
	_, err = fmt.Sscanf(string(content), "%d", &rpFilterValue)
	if err != nil {
		return 0, err
	}

	return rpFilterValue, nil
}

// setRpFilterSetting sets the rp_filter setting for the interface
func setRpFilterSetting(interfaceName string, value int) error {
	filePath := fmt.Sprintf("/proc/sys/net/ipv4/conf/%s/rp_filter", interfaceName)
	return os.WriteFile(filePath, []byte(fmt.Sprintf("%d\n", value)), 0644)
}
