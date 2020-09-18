<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->


- [Submariner](#submariner)
- [Architecture](#architecture)
  - [Network Path](#network-path)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
  - [Installation using subctl](#installation-using-subctl)
    - [Download](#download)
    - [Broker](#broker)
    - [Engine and route agent](#engine-and-route-agent)
  - [Installation using operator](#installation-using-operator)
  - [Manual installation using helm charts](#manual-installation-using-helm-charts)
    - [Setup](#setup)
    - [Broker Installation/Setup](#broker-installationsetup)
    - [Submariner Installation/Setup](#submariner-installationsetup)
      - [Installation of Submariner in each cluster](#installation-of-submariner-in-each-cluster)
  - [Validate Submariner is Working](#validate-submariner-is-working)
- [Building and Testing](#building-and-testing)
- [Known Issues/Notes](#known-issuesnotes)
  - [Openshift Notes](#openshift-notes)
- [Contributing](#contributing)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Submariner

[![End to End Tests](https://github.com/submariner-io/submariner/workflows/End%20to%20End%20Tests/badge.svg)](https://github.com/submariner-io/submariner/actions?query=workflow%3A%22End+to+End+Tests%22+branch%3Amaster)
[![Unit Tests](https://github.com/submariner-io/submariner/workflows/Unit%20Tests/badge.svg)](https://github.com/submariner-io/submariner/actions?query=workflow%3A%22Unit+Tests%22)
[![Linting](https://github.com/submariner-io/submariner/workflows/Linting/badge.svg)](https://github.com/submariner-io/submariner/actions?query=workflow%3ALinting+branch%3Amaster)
[![Release Images](https://github.com/submariner-io/submariner/workflows/Release%20Images/badge.svg)](https://github.com/submariner-io/submariner/actions?query=workflow%3A%22Release+Images%22)
[![Periodic](https://github.com/submariner-io/submariner/workflows/Periodic/badge.svg)](https://github.com/submariner-io/submariner/actions?query=workflow%3APeriodic+branch%3Amaster)

Submariner is a tool built to connect overlay networks of different Kubernetes clusters. While most testing is performed against Kubernetes
clusters that have enabled Flannel/Canal/Weavenet/OpenShiftSDN, Submariner should be compatible with any CNI-compatible cluster network
provider, as it utilizes off-the-shelf components such as strongSwan/Charon to establish IPsec tunnels between each Kubernetes cluster.

Note that Submariner is in the **pre-alpha** stage, and should not be used for production purposes. While we welcome usage/experimentation
with it, it is quite possible that you could run into severe bugs with it, and as such this is why it has this labeled status.

# Architecture

See the [Architecture docs on Submainer's website](https://submariner.io/architecture/).

## Network Path

The network path of Submariner varies depending on the origin/destination of the IP traffic. In all cases, traffic between two clusters will
transit between the leader elected (in each cluster) gateway nodes, through `ip xfrm` rules. Each gateway node has a running Charon daemon
which will perform IPsec keying and policy management.

When the source pod is on a worker node that is not the elected gateway node, the traffic destined for the remote cluster will transit
through the submariner VxLAN tunnel (`vx-submariner`) to the local cluster gateway node. On the gateway node, traffic is encapsulated in an
IPsec tunnel and forwarded to the remote cluster. Once the traffic reaches the destination gateway node, it is routed in one of two ways,
depending on the destination CIDR. If the destination CIDR is a pod network, the traffic is routed via CNI-programmed network. If the
destination CIDR is a service network, then traffic is routed through the facility configured via kube-proxy on the destination gateway
node.

# Prerequisites

See the [Prerequisites docs on Submainer's website](https://submariner.io/quickstart/#prerequisites).

# Installation

Submariner supports a number of different deployment models. An Operator is provided to manage Submariner deployments. The Operator can be
deployed via the `subctl` CLI helper utility, or via Helm charts, or directly. Submariner can also be deployed directly, without the
Operator, via Helm charts.

## Installation using Operator via subctl

Submairner provides the `subctl` CLI utility to simplify the deployment and maintenance of Submariner across your clusters.

See the [`subctl` docs on Submainer's website](https://submariner.io/deployment/subctl/).

## Installation using Helm

Submariner's Helm-based installs are currently being refactored.

See [submariner-operator#695](https://github.com/submariner-io/submariner-operator/issues/695) for the plan.

Updated docs will be in the [Helm Deployments](https://submariner.io/deployment/helm/) section of Submariner's website.

## Validate Submariner is Working

Switch to the context of one of your clusters, i.e. `kubectl config use-context west`

Run an nginx container in this cluster, i.e. `kubectl run -n default nginx --image=nginx`

Retrieve the pod IP of the nginx container, looking under the "Pod IP" column for `kubectl get pod -n default`

Change contexts to your other workload cluster, i.e. `kubectl config use-context east`

Run a busybox pod and ping/curl the nginx pod:

```shell
kubectl run -i -t busybox --image=busybox --restart=Never
```

If you don't see a command prompt, try pressing enter.

```shell
ping <NGINX_POD_IP>
wget -O - <NGINX_POD_IP>
```

# Building and Testing

See the [Building and Testing docs on Submainer's website](https://submariner.io/contributing/building_testing/).

# Known Issues/Notes

## Openshift Notes

When running in Openshift, we need to grant the appropriate security context for the service accounts

   ```shell
   oc adm policy add-scc-to-user privileged system:serviceaccount:submariner:submariner-routeagent
   oc adm policy add-scc-to-user privileged system:serviceaccount:submariner:submariner-engine
   ```

# Contributing

See the [Contributing docs on Submainer's website](https://submariner.io/contributing/).
