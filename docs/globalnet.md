# Introduction

Submariner is a tool build to connect overlay networks of different Kubernetes clusters. These clusters can be on different public clouds or OnPremises. An important use case for Submariner is to connect disparage independent clusters into a single cohesive multi-cluster.
 However, one key limitation of submariner's current design is that it doesn't support overlapping CIDRs across these clusters. Each cluster must use a distinct ClusterCIDR that doesn't conflict or overlap with any other cluster that is going to be part of multicluster.

<!-- TODO: Add diagram showing the problem --> 

This is largely problematic coz most actual deployments use the default CIDR for cluster, this end up using the same CIDR. Changing CIDR on existing clusters is a very disruptive process and requires a cluster restart. So we require a means to allow clusters with Overlapping CIDRs connect together with submariner.

# Architecture

To support overlapping CIDRs in clusters connected through submariner, we're introducing a new component called Global Private Network, or GlobalNet (`globalnet`). This GlobalNet is a virtual network with scope as submariner multi-cluster and CIDR 169.254.0.0/16. Each cluster is given a subnet from this Global Private Network, configured as new cluster parameter `GlobalCIDR`.

Once configured, each service and pod that requires cross cluster access is allocated an IP, `GlobalIp` from this `GlobalCIDR` and it is annotated to the POD/Service object. This `GlobalIp` is used for all cross-cluster communication from and to this POD/Service. Routing and IPTable/OVS/OVN rules are configured to use this IP for Ingress/Egress. All address translations occur at Gateway node.

This is achieved by a new controller, Submariner Globalnet.

## submariner-globalnet

Submariner GlobalNet provides cross cluster connectivity between pods and services using GlobalIp. Compiled as binary `submariner-globalnet`, it is the controller that is responsible for maintaining and allocating pool of globalIPs, annotating services and pods with their globalIp, and finally configuring the required rules on gateway node to provide cross cluster connectivity using GlobalIp. It mainly consists of two key components.

### IP Address Manager

First key component of Globalnet controller is IP Address Manager (IPAM). This module does the following:

* Create a pool of IP addresses based on `GlobalCIDR` configured on cluster.
* Whenever a Pod/Service is created, allocate a GlobalIp from the GlobalIp pool.
* Annotate the Pod/Service with the `submariner.io/GlobalIp=<global-ip>`.
* Whenever Pod/Service is deleted, release the IP allocated from the GlobalIp pool.

### Globalnet

This is the core component and is responsible for programming the routing entries and IPTable rules. This module does the following:

* Creates initial IPTables chains for Globalnet rules.
* Whenever a pod is annotated with Global IP, creates an egress SNAT rule to convert source ip from PodIp to pod's GlobalIp.
* Whenever a service is annotated with Global IP, creates an ingress rule to direct all traffic destined to service's GlobalIp to the service's `kube-proxy` IPTables chain which in turn directs traffic to ServiceIp.
* On deletion of pod/service, clean up the rules from the gateway node.

Since this implementation currently relies on KubeProxy, globalnet only works with deployments that use `kube-proxy`

<!-- TODO: Add a block diagram showing the solution -->

## Service Discovery - Lighthouse

Connectivity is only part of solution, pods still need to know the IPs of services on remote clusters.

This is achieved by enhancing [lighthouse](https://github.com/submariner-io/lighthouse) with support for GlobalNet. Lighthouse controller adds the service's GlobalIp to the `MultiClusterSerivce` object that is distributed to all clusters. The [lighthouse plugin](https://github.com/submariner-io/lighthouse/tree/master/plugin/lighthouse) then uses this Service's GlobalIp when replying to DNS queries for the service.

# Installation

TBD.

# Testing

Refer [Testing guide](testing.md) for instructions on how to test, same steps apply.

# Known Issues

# Building

Nothing extra needs to be done to build `submariner-globalnet`. Standard submariner build will now build `submariner-globalnet` binary too.
# TODO

Not everything mentioned in architecture is currently implemented. This section lists work items that are pending.

* Operator support for globalnet installation
* Helm support for globalnet installation
* `subctl` support for globalnet
* Lighthouse changes for globalnet
* User guide
