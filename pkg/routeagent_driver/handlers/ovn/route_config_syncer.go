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

package ovn

import (
	"net"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	iptcommon "github.com/submariner-io/submariner/pkg/routeagent_driver/iptables"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

func (ovn *Handler) startRouteConfigSyncer(stop chan struct{}) {
	logger.Info("Starting route config syncer")
	// Start monitoring routes
	go wait.Until(func() {
		err := ovn.monitorRoutingTable(stop)
		if err != nil {
			logger.Errorf(err, "error starting monitorRoutingTable for interface: %q", OVNK8sMgmntIntfName)
		}
	}, time.Second, stop)
}

func (ovn *Handler) monitorRoutingTable(stop chan struct{}) error {
	iface, err := netlink.LinkByName(OVNK8sMgmntIntfName)
	if err != nil {
		return errors.Wrapf(err, "failed to find interface: %q", OVNK8sMgmntIntfName)
	}

	addrCh := make(chan netlink.AddrUpdate)
	doneCh := make(chan struct{})

	err = netlink.AddrSubscribe(addrCh, doneCh)
	if err != nil {
		return errors.Wrapf(err, "failed to subscribe to address updates")
	}

	defer close(doneCh)

	var prevIP net.IP

	assignPrevIP := func() {
		addrs, err := netlink.AddrList(iface, netlink.FAMILY_V4)
		if err != nil {
			logger.Warningf("Failed to get the IP4 address list for interface %q: %v", OVNK8sMgmntIntfName, err)
		} else if len(addrs) > 0 {
			prevIP = addrs[0].IP
		}
	}

	handleAddressChange := func(addrUpdate netlink.AddrUpdate) {
		if addrUpdate.LinkIndex != iface.Attrs().Index {
			return
		}

		if !addrUpdate.NewAddr {
			// Address deleted
			prevIP = nil
			return
		}

		addrs, err := netlink.AddrList(iface, netlink.FAMILY_V4)
		if err != nil {
			logger.Warningf("Failed to get the IP4 address list for interface %q: %v", OVNK8sMgmntIntfName, err)
			return
		}

		if len(addrs) == 0 {
			return
		}

		newIP := addrs[0].IP
		// If the new IP address is not nil and different from the previous IP address,
		// which mean an IP address is assigned to the interface. When ovnkube-node pod restarts
		// the IP address is first cleared and then assigned again.
		if newIP != nil && !newIP.Equal(prevIP) {
			err := ovn.handleInterfaceAddressChange()
			if err != nil {
				logger.Errorf(err, "error handling interface address addition: %s", newIP.String())

				prevIP = nil // Reset previous IP to nil in case of error
			} else {
				prevIP = newIP
			}
		}
	}

	assignPrevIP()

	for {
		select {
		case <-stop:
			return nil
		case addrUpdate := <-addrCh:
			handleAddressChange(addrUpdate)
		}
	}
}

func (ovn *Handler) handleInterfaceAddressChange() error {
	backoff := wait.Backoff{
		Cap:      3 * time.Minute,
		Duration: 5 * time.Second,
		Factor:   1.2,
		Steps:    24,
	}

	var err error

	retryErr := retry.OnError(backoff, func(err error) bool {
		logger.Infof("Waiting for interface %q to be ready: %v", OVNK8sMgmntIntfName, err)
		return true
	}, func() error {
		ovn.mutex.Lock()
		defer ovn.mutex.Unlock()
		if ovn.isGateway {
			err = ovn.updateGatewayDataplane()
			if err != nil {
				return errors.Wrap(err, "error syncing gateway routes")
			}
		}

		err = ovn.updateHostNetworkDataplane()
		if err != nil {
			return errors.Wrap(err, "error syncing host network routes")
		}

		if err := iptcommon.InitSubmarinerPostRoutingChain(ovn.ipt); err != nil {
			return errors.Wrapf(err, "error syncing IPtable %q chain", constants.PostRoutingChain)
		}

		return nil
	})

	return errors.Wrap(retryErr, "error syncing the config even after multiple retries")
}
