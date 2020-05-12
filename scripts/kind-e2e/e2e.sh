#!/usr/bin/env bash

## Process command line flags ##

source /usr/share/shflags/shflags
DEFINE_string 'focus' '.*' 'Ginkgo focus for the E2E tests'
FLAGS "$@" || exit $?
eval set -- "${FLAGS_ARGV}"

focus="${FLAGS_focus}"
echo "Running with: focus=${focus}"

set -em

source ${SCRIPTS_DIR}/lib/debug_functions
source ${SCRIPTS_DIR}/lib/version
source ${SCRIPTS_DIR}/lib/utils

### Variables ###

E2E_DIR=${DAPPER_SOURCE}/scripts/kind-e2e/

### Functions ###

function deploy_env_once() {
    if with_context cluster3 kubectl wait --for=condition=Ready pods -l app=submariner-engine -n "${SUBM_NS}" --timeout=3s > /dev/null 2>&1; then
        echo "Submariner already deployed, skipping deployment..."
        return
    fi

    make deploy
    declare_kubeconfig
}

function test_with_e2e_tests {
    set -o pipefail 

    cd ${DAPPER_SOURCE}/test/e2e

    go test -v -args -ginkgo.v -ginkgo.randomizeAllSpecs \
        -submariner-namespace $SUBM_NS -dp-context cluster2 -dp-context cluster3 -dp-context cluster1 \
        -ginkgo.noColor -ginkgo.reportPassed \
        -ginkgo.focus "\[${focus}\]" \
        -ginkgo.reportFile ${DAPPER_OUTPUT}/e2e-junit.xml 2>&1 | \
        tee ${DAPPER_OUTPUT}/e2e-tests.log
}

### Main ###

declare_kubeconfig

deploy_env_once
test_with_e2e_tests

cat << EOM
Your 3 virtual clusters are deployed and working properly with your local submariner source code, and can be accessed with:

export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kubeconfigs/kind-config-cluster{1..3} | sed 's/ /:/g')

$ kubectl config use-context cluster1 # or cluster2, cluster3..

To clean evertyhing up, just run: make cleanup
EOM
