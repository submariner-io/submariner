# WireGuard Cable Driver (WIP)

[WireGuard](https://www.wireguard.com "Wireguard homepage") is an extremely simple yet fast and modern VPN that utilizes state-of-the-art cryptography. 

Traffic is encrypted and encapsulated in UDP packets.

## Driver design

- WireGuard creates a virtual network device which is created and accessesed through netlink. It looks like any network device and currenly has a hardcoded name `subwg0`.

- WireGuard identifies peers by their cryptographic public key (no need to exchange shared secrets) -- owner must have the corresponding private key to prove idenity

- The driver creates the key pair and adds the public key to the local endpoint so other clusters can connect. Like `ipsec`, the node IP address is used as the endpoint udp address of the WireGuard tunnels. A fixed (hardcoded) port is used for all endpoints.

- The drivers adds routing rules to redirect cross cluster communication through `subwg0`. 
  (*note: this is different from `ipsec`, which intercepts packets at netfilter level.*)

- The driver uses [`wgctrl`](https://github.com/WireGuard/wgctrl-go "WgCtrl github"), a go package that enables control of WireGuard devices on multiple platforms. Link creation and removal are done through [`netlink`](https://github.com/vishvananda/netlink "Netlink github").

## Installation

- WireGuard needs to be [installed](https://www.wireguard.com/install "WireGuard installation instructions") on the gateway nodes. For example, (Ubuntu < 19.04),  
  ```ShellSession
  $ sudo add-apt-repository ppa:wireguard/wireguard
  $ sudo apt-get update
  $ sudo apt-get install wireguard
  ```
   
- The driver needs to be enbaled with 
  ```ShellSession
  $ bin/subctl join --kubeconfig wg3-conf --cable-driver wireguard --disable-nat  --repository roytman --version  latest broker-info.subm
  ```

## Troubleshooting, limitations
    
- If you get the following message 
  ```
  Fatal error occurred creating engine: failed to add wireguard device: operation not supported
  ```
  you probably did not install WireGuard on the Gateway node.
  
- Support for e2e testing with `kind` is not iplemented yet. The e2e tests can be run with WireGuard by setting it as the default driver in `pkg/cable/wireguard/WGdriver.go` **and** unsetting StrogSwan in `pkg/cable/ipsec/strongswan.go` 
  ```GoLang
  func init() {
    // uncomment next line to set as default
    //cable.SetDefautCableDriver(cableDriverName)
    cable.AddDriver(cableDriverName, NewWGDriver)
  }

  ```

- No new `iptables` rules were added, although source NAT needs to be disabled for cross cluster communication. This is similar to disabling SNAT when sending cross-cluster traffic between nodes to `submariner-gateway`, so the existing rules should be enough.
  **The driver will fail if the CNI does SNAT before routing to Wireguard** (e.g., failed with Calico, works with Flannel).
  
  
