#!/usr/bin/env bash

## Process command line flags ##

source /usr/share/shflags/shflags
DEFINE_string 'deploytool' 'operator' 'Tool to use for deploying (operator/helm)'
FLAGS "$@" || exit $?
eval set -- "${FLAGS_ARGV}"

deploytool="${FLAGS_deploytool}"
echo "Running with: deploytool=${deploytool}"

set -em

source ${SCRIPTS_DIR}/lib/debug_functions
source ${SCRIPTS_DIR}/lib/version
source ${SCRIPTS_DIR}/lib/utils

### Variables ###

E2E_DIR=${DAPPER_SOURCE}/scripts/kind-e2e/

### Functions ###

# TODO: Copied from shipyard since deploytool determines the namespace, we should fix operator to use the same namespace (or receive it).
function load_deploytool() {
    local deploy_lib=${SCRIPTS_DIR}/lib/deploy_${deploytool}
    if [[ ! -f $deploy_lib ]]; then
        echo "Unknown deploy method: ${deploytool}"
        exit 1
    fi

    echo "Will deploy submariner using ${deploytool}"
    . $deploy_lib
}

function test_with_e2e_tests {
    set -o pipefail 

    cd ${DAPPER_SOURCE}/test/e2e

    go test -v -args -ginkgo.v -ginkgo.randomizeAllSpecs \
        -submariner-namespace $SUBM_NS -dp-context cluster2 -dp-context cluster3 -dp-context cluster1 \
        -ginkgo.noColor -ginkgo.reportPassed \
        -ginkgo.reportFile ${DAPPER_OUTPUT}/e2e-junit.xml 2>&1 | \
        tee ${DAPPER_OUTPUT}/e2e-tests.log
}

function cleanup {
    "${SCRIPTS_DIR}"/cleanup.sh
}

### Main ###

declare_kubeconfig

load_deploytool

test_with_e2e_tests

cat << EOM
Your 3 virtual clusters are deployed and working properly with your local submariner source code, and can be accessed with:

export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kubeconfigs/kind-config-cluster{1..3} | sed 's/ /:/g')

$ kubectl config use-context cluster1 # or cluster2, cluster3..

To clean evertyhing up, just run: make cleanup
EOM
