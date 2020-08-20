<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Testing in pre-existing clusters](#testing-in-pre-existing-clusters)
- [Testing with E2E](#testing-with-e2e)
  - [Prerequisites](#prerequisites)
  - [Installation and usage](#installation-and-usage)
    - [Operator](#operator)
      - [Reloading your code changes](#reloading-your-code-changes)
    - [Re-running e2e tests after your code changes](#re-running-e2e-tests-after-your-code-changes)
      - [Cleanup](#cleanup)
    - [Full example](#full-example)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Testing in pre-existing clusters

E2E testing purpose is to validate submariner behaviour from an integration point of
view. It needs to be executed in connection to an existing set of clusters.

The E2E tests require kubeconfigs for at least 2 dataplane clusters. By dataplane
clusters we mean clusters which are interconnected by a deployment of submariner.

E2E tests identify each cluster by their associated kubeconfig context names,
so you need to specify the -dp-context flag for each context name you want
the E2E tests to use.

Assuming that we have cluster1, cluster2 and cluster3 contexts, and that
cluster1 is our broker cluster, **to execute the E2E tests** we would do:

  ```bash
  export GO111MODULE=on
  cd test/e2e
  go test -args -kubeconfig=creds/cluster1:creds/cluster2:creds/cluster3 \
                -dp-context cluster2 \
                -dp-context cluster3 \
                -ginkgo.randomizeAllSpecs
  ```

The -kubeconfig flag can be ommited if the KUBECONFIG environment variable
is set to point to the kubernetes config files.

  ```bash
  export KUBECONFIG=creds/cluster1:creds/cluster2:creds/cluster3
  ```

If you want to execute just a subset of the available E2E tests, you can
specify the ginkgo.focus argument

  ```bash
  export GO111MODULE=on
  cd test/e2e
  go test -args -kubeconfig=creds/cluster1:creds/cluster2:creds/cluster3 \
                -dp-context cluster2 \
                -dp-context cluster3 \
                -ginkgo.focus=dataplane \
                -ginkgo.randomizeAllSpecs
  ```

It's possible to generate jUnit XML report files

  ```bash
  export GO111MODULE=on
  cd test/e2e
  go test -args  -kubeconfig=creds/cluster1:creds/cluster2:creds/cluster3 \
                 -dp-context cluster2 \
                 -dp-context cluster3 \
                 -ginkgo.v -ginkgo.reportPassed -ginkgo.reportFile ./junit -ginkgo.randomizeAllSpecs
  ```

Suggested arguments

  ```text
  -test.v       : verbose output from go test
  -ginkgo.v     : verbose output from ginkgo
  -ginkgo.trace : output stack track on failure
  -ginkgo.randomizeAllSpecs  : prevent test-ordering dependencies from creeping in
  ```

It may be helpful to use the [delve debugger](https://github.com/derekparker/delve)
to gain insight into the test:

  ```bash
  export GO111MODULE=on
  cd test/e2e
  dlv test
  ```

  When using delve please note, the equivalent of `go test -args` is `dlv test --`,
  dlv test treats both single and double quotes literally.
  Neither `-ginkgo.focus="mytest"` nor `-ginkgo.focus='mytest'` will match `mytest`
  `-ginkgo.focus=mytest` is required, for example:

  ```bash
  export GO111MODULES=on
  cd test/e2e
  dlv test -- -ginkgo.v -ginkgo.focus=mytest
  ```

# Testing with E2E

The e2e environment can be used for local testing, development, and CI purposes.

E2E environment consists of:

- 3 k8s clusters deployed with [kind].
  - Cluster1: One master node for broker.
  - Cluster{2..3}: One master node and workers for gateways nodes.
- Submariner installed and configured on top of the clusters.

[kind] is a tool for running local Kubernetes clusters using Docker container “nodes”.

## Prerequisites

- [docker]
- [kubectl]
- [Add local user to docker group]

Optional useful tools for troubleshooting:

- [k9s]
- [kubetail]

## Installation and usage

To run the tests simply execute the following:

```bash
make e2e
```

To test with optional functionality, use the `using=` flag to enable the extra options.

Currently, these options are supported:

- **globalnet:** To deploy with globalnet (and overlapping IPs)
- **helm:** To deploy using helm instead of the operator.
- **libreswan:** To use libreswan to establish connectivity.
- **wireguard:** To use wireguard to establish connectivity.

For example:

```bash
make e2e using=globalnet,wireguard,helm
```

After a permanent run completes, the configuration for the running clusters can be found inside **output/kubeconfigs** folder.
You can export the kube configs in order to interact with the clusters.

```bash
export KUBECONFIG=$(echo $(git rev-parse --show-toplevel)/output/kubeconfigs/kind-config-cluster{1..3} | sed 's/ /:/g')
```

List the contexts:

```bash
kubectl config list-contexts
```

You should be able to see 3 contexts. From this stage you can interact with the clusters
as with any normal k8s cluster.

**NOTE**: Each time **make e2e** command is executed, the local code will be build, pushed to kind clusters
as docker images, submariner will be redeployed on the clusters from pushed images and E2E tests will be executed.
This mode allows the developers to test their local code fast on a very close to real world scenario setup.

**NOTE**: If you only want to create the test environment without running the e2e tests, you can do it by executing
the following command:

```bash
make deploy
```

### Operator

After generating the Operator by running `make build-operator`, your newly generated operator
is automatically fully integrated into the Submariner CI automation. Simply use
the `deploytool` flag to the standard `make` commands.

```make e2e deploytool=operator```

A large set of verifications for the Operator and the resulting Submariner
deployment will automatically run during and after the deployment.

#### Reloading your code changes

During the development of new features you may want to compile submariner and push the images
into the local registry used by the kind clusters. You can use the reload-images make target
for that.

This target depends on the build and packaging of the container images. It will push
the new images to the local registry and restart all the services (gateway, routeagent, globalnet).

```bash
make reload-images
```

If you are working on a specific service, you can specify to only restart that service, for example:

```bash
make reload-images restart=gateway
```

If you don't want to restart any service because you plan to restart specific pods in specific clusters
manually, then run:

```bash
make reload-images restart=none
```

### Re-running e2e tests after your code changes

Once your kind virtual clusters and submariner are deployed, you can re-run e2e just by repeating the `make e2e` call.
Deployment and install will be avoided, hence the existing environment will remain as is.
If you need to load new container images, please check `make reload-images` from the previous section.

In case you want to re-run just the tests:

```bash
make e2e
```

In case you want to use updated images and re-run the tests:

```bash
make reload-images e2e
```

You can focus the e2e tests on specific tags by using the focus makefile env. The following
example runs only the tests tagged as [redundancy].

```bash
make e2e focus=redundancy
```

#### Cleanup

At any time you can run a cleanup command that will remove all the kind clusters.

```bash
make cleanup
```

You can do full docker cleanup, but it will force all the docker images to be removed and invalidate the local docker cache.
The next run will be a cold run and will take more time.

```bash
docker system prune --all
```

### Full example

```bash
make e2e version=1.14.1 globalnet=true deploytool=operator
```

<!--links-->
[kind]: https://github.com/kubernetes-sigs/kind
[docker]: https://docs.docker.com/install/
[kubectl]: https://kubernetes.io/docs/tasks/tools/install-kubectl/
[k9s]: https://github.com/derailed/k9s
[kubetail]: https://github.com/johanhaleby/kubetail
[kind release page]: https://github.com/kubernetes-sigs/kind/releases
[Add local user to docker group]: https://docs.docker.com/install/linux/linux-postinstall/
