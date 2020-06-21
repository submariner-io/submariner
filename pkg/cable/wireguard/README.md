# WireGuard Cable Driver

[WireGuard](https://www.wireguard.com "WireGuard homepage") is an extremely simple yet fast and modern VPN that utilizes state-of-the-art cryptography.

Traffic is encrypted and encapsulated in UDP packets.

## Driver design

- WireGuard creates a virtual network device that is accessed via netlink. It appears like any network device and currently has a hardcoded name `subwg0`.

- WireGuard identifies peers by their cryptographic public key without the need to exchange shared secrets. The owner of the public key must have the corresponding private key to prove identity.

- The driver creates the key pair and adds the public key to the local endpoint so other clusters can connect. Like `ipsec`, the node IP address is used as the endpoint udp address of the WireGuard tunnels. A fixed port is used for all endpoints.

- The driver adds routing rules to redirect cross cluster communication through the virtual network device `subwg0`.
  (*note: this is different from `ipsec`, which intercepts packets at netfilter level.*)

- The driver uses [`wgctrl`](https://github.com/WireGuard/wgctrl-go "WgCtrl github"), a go package that enables control of WireGuard devices on multiple platforms. Link creation and removal are done through [`netlink`](https://github.com/vishvananda/netlink "Netlink github"). Currently assuming Linux Kernel WireGuard (`wgtypes.LinuxKernel`).

## Installation

- WireGuard needs to be [installed](https://www.wireguard.com/install "WireGuard installation instructions") on the gateway nodes. For example, (Ubuntu < 19.04),
  ```ShellSession
  $ sudo add-apt-repository ppa:wireguard/wireguard
  $ sudo apt-get update
  $ sudo apt-get install linux-headers-`uname -r` -y
  $ sudo apt-get install wireguard
  ```

- The driver needs to be enabled with
  ```ShellSession
  $ bin/subctl join --cable-driver wireguard --disable-nat broker-info.subm
  ```

- The default UDP listen port for WireGuard is `5871`. It can be changed by setting the env var `CE_IPSEC_NATTPORT`

## Troubleshooting, limitations

- If you get the following message
  ```
  Fatal error occurred creating engine: failed to add wireguard device: operation not supported
  ```
  you probably did not install WireGuard on the Gateway node.

- The e2e tests can be run with WireGuard by setting `DEPLOY_ARGS` before calling `make e2e`
  ```ShellSession
  $ export DEPLOY_ARGS="--deploytool operator --deploytool_submariner_args '--cable-driver=wireguard'"
  ```

- No new `iptables` rules were added, although source NAT needs to be disabled for cross cluster communication. This is similar to disabling SNAT when sending cross-cluster traffic between nodes to `submariner-gateway`, so the existing rules should be enough.
  **The driver will fail if the CNI does SNAT before routing to Wireguard** (e.g., failed with Calico, works with Flannel).
