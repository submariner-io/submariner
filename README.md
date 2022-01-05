# Submariner

<!-- markdownlint-disable line-length -->
[![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/4865/badge)](https://bestpractices.coreinfrastructure.org/projects/4865)
[![Release Images](https://github.com/submariner-io/submariner/workflows/Release%20Images/badge.svg)](https://github.com/submariner-io/submariner/actions?query=workflow%3A%22Release+Images%22)
[![Periodic](https://github.com/submariner-io/submariner/workflows/Periodic/badge.svg)](https://github.com/submariner-io/submariner/actions?query=workflow%3APeriodic)
[![Flake Finder](https://github.com/submariner-io/submariner/workflows/Flake%20Finder/badge.svg)](https://github.com/submariner-io/submariner/actions?query=workflow%3A%22Flake+Finder%22)
<!-- markdownlint-enable line-length -->

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Architecture](#architecture)
  - [Network Path](#network-path)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
  - [Installation using subctl](#installation-using-subctl)
  - [Installation using Helm](#installation-using-helm)
  - [Validate Submariner is Working](#validate-submariner-is-working)
- [Building and Testing](#building-and-testing)
- [Known Issues](#known-issues)
- [Contributing](#contributing)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

Submariner is a tool built to connect overlay networks of different Kubernetes clusters. While most testing is performed against Kubernetes
clusters that have enabled Flannel/Calico/Canal/Weave/OpenShiftSDN, Submariner should be compatible with most CNI cluster network
providers, as it utilizes off-the-shelf components to establish encrypted tunnels between each Kubernetes cluster.

Note that Submariner is in an early stage, and while we welcome usage and experimentation, it is quite possible that you could run into
bugs.

Submariner is a Cloud Native Computing Foundation sandbox project.

## Architecture

See the [Architecture section](https://submariner.io/getting-started/architecture/) of Submariner's website.

### Network Path

The network path of Submariner varies depending on the origin/destination of the IP traffic. In all cases, traffic between two clusters will
transit between the leader elected (in each cluster) gateway nodes, through `ip xfrm` rules. Each gateway node has a running Charon daemon
which will perform IPsec keying and policy management.

When the source Pod is on a worker node that is not the elected gateway node, the traffic destined for the remote cluster will transit
through the submariner VXLAN tunnel (`vx-submariner`) to the local cluster gateway node. On the gateway node, traffic is encapsulated in an
IPsec tunnel and forwarded to the remote cluster. Once the traffic reaches the destination gateway node, it is routed in one of two ways,
depending on the destination CIDR. If the destination CIDR is a Pod network, the traffic is routed via CNI-programmed network. If the
destination CIDR is a Service network, then traffic is routed through the facility configured via kube-proxy on the destination gateway
node.

## Prerequisites

See the [Prerequisites docs](https://submariner.io/getting-started/#prerequisites) on Submariner's website.

## Installation

Submariner is deployed and manged by its Operator. The Operator can be deployed directly, or by using Submariner's Helm Charts, or by using
Submariner's `subctl` CLI helper utility. `subctl` is the recommended deployment method because it has the most refined deployment user
experience and additionally provides testing and bug-diagnosing capabilities.

### Installation using subctl

Submariner provides the `subctl` CLI utility to simplify the deployment and maintenance of Submariner across your clusters.

See the [`subctl` Deployment docs](https://submariner.io/operations/deployment/subctl/) on Submariner's website.

### Installation using Helm

See the [Helm Deployment docs](https://submariner.io/operations/deployment/helm/) on Submariner's website.

### Validate Submariner is Working

See the [`subctl verify` docs](https://submariner.io/operations/deployment/subctl/#verify) and [Automated
Troubleshooting docs](https://submariner.io/operations/troubleshooting/#automated-troubleshooting) on Submariner's website.

## Building and Testing

See the [Building and Testing docs](https://submariner.io/development/building-testing/) on Submariner's website.

## Known Issues

See the [Known Issues docs](https://submariner.io/operations/known-issues/) on Submariner's website.

## Contributing

See the [Development section](https://submariner.io/development/) of Submariner's website.
