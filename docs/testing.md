<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [E2E testing](#e2e-testing)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

### E2E testing

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
                 -ginkgo.v -ginkgo.reportPassed -report-dir ./junit -ginkgo.randomizeAllSpecs
  ```

Suggested arguments
  ```
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

To run all e2e tests in a [Kind deployment with 3 clusters](https://github.com/submariner-io/submariner/blob/master/scripts/kind-e2e/README.md), you can trigger `make e2e` command. 