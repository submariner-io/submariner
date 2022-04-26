# Submariner Multiple Active Gateways Proof Of Concept

This Repo houses the core work for the Multiple Active Gateways Proof of concept, please see [the
associated EP](https://github.com/submariner-io/enhancements/pull/69) for a more detailed
design.

## QUICK START

***This is assumed you have the
[associated submariner-operator feature branch](https://github.com/submariner-io/submariner-operator/tree/feature-multi-active-gw)
built locally as well

1. Start your deployment with MAG enabled

    To setup a standard MAG deployment

    ```bash

    make deploy DEPLOY_ARGS='--deploytool_submariner_args="--multi-active-gateway=true" --deploytool_broker_args "--components
    service-discovery,connectivity" --cable_driver vxlan'

    ```

    To setup a Globalnet MAG deployment

    ```bash

    make deploy DEPLOY_ARGS='--deploytool_submariner_args="--multi-active-gateway=true" --deploytool_broker_args "--components
    service-discovery,connectivity" --cable_driver vxlan'

    ```

2. Spin up Multiple Gateways on each cluster

    ```bash

    kubectl config use-context cluster1

    kubectl label node cluster1-worker2 submariner.io/gateway=true

    kubectl config use-context cluster2

    kubectl label node cluster2-worker2 submariner.io/gateway=true

    ```

3. Run flow tests

  ```bash

  ## Clone Traffic flow repo

  git clone <https://github.com/Billy99/ovn-kuber-traffic-flow-tests.git> && cd ovn-kuber-traffic-flow-tests

  # Launch flow tester resources

  ./mclaunch.sh

  # Run tests

  ./mctest.sh

  ```

## Implementation Details and Current Code Status (04/26/2022)

* The [MAG POC](https://github.com/submariner-io/submariner/pull/1794) was architected to allow for some or all of the
aforementioned features to be merged in smaller chunks as opposed to a single large commit, however it is still a fairly
significant design overhaul and certain implementation details should be agreed upon before moving forward.

* Ultimately we would recommend that the work be agreed upon/implemented/merged in the following chunks using the code
already committed to the MAG feature branch as a guide, rather than simply trying to merge the entire branch.

1. Standardize the VXLAN architecture

    The MAG POC standardizes the generic `vx-submariner`(intra-cluster) an `vxlan-tunnel`(inter-cluster) setup which would allow
    a common library to be written for the setup, configuration, and FDB management for ALL submariner created VXLAN tunnel
    interfaces. Currently the POC does not write this library, but it provides an example and proves that the design change
    would function correctly for all existing traffic flow scenarios.

2. Move to level driven reconcilers

    As [recommended by K8s](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-api-machinery/controllers.md),
    We suggest that for linux networking resources such as routes, iptables-rules, and FDB entries with significant state
    change that Submariner moves to utilizing level driven reconciliation design.  Such controller design became a necessity
    for the MAG POC since "change of state" occurs frequently, which can easily result in errors with the existing edge driven
    reconcilers. The POC code re-writes the FDB and route reconciler in the Route-Agent to be level driven, however in the future,
    a generic reconciler library could be written and shared across submariner libraries.

3. IPTABLES rule architecture

    While writing the MAG POC we discovered that there are many scenarios where submariner traffic traverses CNI/Kubeproxy
    chains in places where it did not need to.  This makes the Submariner IPTABLES rule architecture a bit confusing and
    could be inefficient especially where connections are inadvertently tracked.  We recommend that the existing architecture
    first be documented(happy to help) so that it can be revised upon. Following a clear documentation effort, the architecture
    can be re-evaluated as needed.  For the MAG POC we generally need to follow the rule that the Submariner Iptables rules
    should operate at a level "above" any CNI/Proxy related rule architecture, so that submariner traffic can "skip" passing
    though any unnecessary chains. This mainly was a problem for us since with MAG, traffic could now traverse clusters via
    different GWs, resulting in broken connection tracking state, and dropped connections.

4. Multi-path Routing Changes

    For MAG multi-path routing needed to be added internally for both intra-cluster traffic (handled by the Route-agent)
    and inter-cluster traffic (handled by the GW/cabledriver).  This work is fairly well split into individual commits within
    the POC but is dependent on 2. It marks the last set of bits needed for Submariner to support running multiple active
    gateways in the standard (non-globalnet) deployment.

5. Globalnet Refactoring

    This is by far the largest code change required by the MAG POC and is the bit which would required most redesign/effort
    to make it's way back into the devel branch. In the POC code the globalnet architecture was changed a the main controllers
    needed for simple Cluster -> Remote service connectivity were implemented.  However, the code is far from complete
    and many more bits, such as unit testing/architecture agreement, need to be completed before moving forward.
