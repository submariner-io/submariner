#!/usr/bin/env bash
set -em

source ${SCRIPTS_DIR}/lib/debug_functions
source ${SCRIPTS_DIR}/lib/version
source ${SCRIPTS_DIR}/lib/utils

### Variables ###

E2E_DIR=${DAPPER_SOURCE}/scripts/kind-e2e/

### Functions ###

function enable_logging() {
    cluster=cluster1

    if kubectl rollout status deploy/kibana > /dev/null 2>&1; then
        echo Elasticsearch stack already installed, skipping...
        return
    fi

    echo Installing Elasticsearch...
    es_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster1-control-plane | head -n 1)
    kubectl apply -f ${E2E_DIR}/logging/elasticsearch.yaml
    kubectl apply -f ${E2E_DIR}/logging/filebeat.yaml
    echo Waiting for Elasticsearch to be ready...
    kubectl wait --for=condition=Ready pods -l app=elasticsearch --timeout=300s
    for i in 2 3; do
        cluster=cluster$i
        kubectl apply -f ${E2E_DIR}/logging/filebeat.yaml
        kubectl set env daemonset/filebeat -n kube-system ELASTICSEARCH_HOST=${es_ip} ELASTICSEARCH_PORT=30000
    done
}

function enable_kubefed() {
    cluster=cluster1
    KUBEFED_NS=kube-federation-system
    if kubectl rollout status deploy/kubefed-controller-manager -n ${KUBEFED_NS} > /dev/null 2>&1; then
        echo Kubefed already installed, skipping setup...
        return
    fi

    helm init --client-only
    helm repo add kubefed-charts https://raw.githubusercontent.com/kubernetes-sigs/kubefed/master/charts
    helm --kube-context cluster1 install kubefed-charts/kubefed --version=0.1.0-rc2 --name kubefed --namespace ${KUBEFED_NS} --set controllermanager.replicaCount=1
    for i in 1 2 3; do
        kubefedctl join cluster${i} --cluster-context cluster${i} --host-cluster-context cluster1 --v=2
        #master_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster${i}-control-plane | head -n 1)
        #kind_endpoint="https://${master_ip}:6443"
        #kubectl patch kubefedclusters -n ${KUBEFED_NS} cluster${i} --type merge --patch "{\"spec\":{\"apiEndpoint\":\"${kind_endpoint}\"}}"
    done
    #kubectl delete pod -l control-plane=controller-manager -n ${KUBEFED_NS}
    echo Waiting for kubefed control plain to be ready...
    kubectl wait --for=condition=Ready pods -l control-plane=controller-manager -n ${KUBEFED_NS} --timeout=120s
    kubectl wait --for=condition=Ready pods -l kubefed-admission-webhook=true -n ${KUBEFED_NS} --timeout=120s
}

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

    cd ../test/e2e

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

LONGOPTS=status:,logging:,kubefed:,deploytool:
# Only accept longopts, but must pass null shortopts or first param after "--" will be incorrectly used
SHORTOPTS=""
! PARSED=$(getopt --options=$SHORTOPTS --longoptions=$LONGOPTS --name "$0" -- "$@")
eval set -- "$PARSED"

while true; do
    case "$1" in
        --status)
            status="$2"
            ;;
        --logging)
            logging="$2"
            ;;
        --kubefed)
            kubefed="$2"
            ;;
        --deploytool)
            deploytool="$2"
            ;;
        --)
            break
            ;;
        *)
            echo "Ignoring unknown option: $1 $2"
            ;;
    esac
    shift 2
done

echo Starting with status: $status, logging: $logging, kubefed: $kubefed, deploytool: $deploytool

declare_kubeconfig

if [[ $status = clean ]]; then
    cleanup
    exit 0
elif [[ $status = onetime ]]; then
    echo Status $status: Will cleanup on EXIT signal
    trap cleanup EXIT
elif [[ $status != keep && $status != create ]]; then
    echo Unknown status: $status
    cleanup
    exit 1
fi

load_deploytool

if [[ $logging = true ]]; then
    enable_logging
fi

if [[ $kubefed = true ]]; then
    # FIXME: Kubefed deploys are broken (not because of this commit)
    enable_kubefed
fi

if [[ $status = keep || $status = onetime ]]; then
    test_with_e2e_tests
fi

if [[ $status = keep || $status = create ]]; then
    echo "your 3 virtual clusters are deployed and working properly with your local"
    echo "submariner source code, and can be accessed with:"
    echo ""
    echo "export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kubeconfigs/kind-config-cluster{1..3} | sed 's/ /:/g')"
    echo ""
    echo "$ kubectl config use-context cluster1 # or cluster2, cluster3.."
    echo ""
    echo "to cleanup, just run: make e2e status=clean"
fi
