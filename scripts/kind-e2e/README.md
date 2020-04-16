# E2E environment

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

To test with a specific k8s version, an additional **version** parameter can be passed to **make e2e** command:
```bash
make ci e2e version=1.14.1
```

Full list of supported k8s versions can found on [kind release page] page. We are using kind vesion 0.6.1.
Default **version** is 1.14.6.

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

#### Full example

```bash
make e2e version=1.14.1 globalnet=true
```

<!--links-->
[kind]: https://github.com/kubernetes-sigs/kind
[docker]: https://docs.docker.com/install/
[kubectl]: https://kubernetes.io/docs/tasks/tools/install-kubectl/
[k9s]: https://github.com/derailed/k9s
[kubetail]: https://github.com/johanhaleby/kubetail
[kind release page]: https://github.com/kubernetes-sigs/kind/releases
[Add local user to docker group]: https://docs.docker.com/install/linux/linux-postinstall/
