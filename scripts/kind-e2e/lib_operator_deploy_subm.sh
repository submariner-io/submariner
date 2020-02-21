#!/bin/bash
# This should only be sourced
if [ "${0##*/}" = "lib_operator_deploy_subm.sh" ]; then
    echo "Don't run me, source me" >&2
    exit 1
fi

### Variables ###

ce_ipsec_ikeport=500
ce_ipsec_nattport=4500
subm_colorcodes=blue
subm_engine_image_repo=local
subm_engine_image_tag=local
subm_ns=submariner-operator

### Functions ###

function get_latest_subctl_tag() {
    curl https://api.github.com/repos/submariner-io/submariner-operator/releases | jq -r '.[0].tag_name'
}

function travis_retry() {
    # We don't pretend to support commands with multiple words
    $1 || (sleep 2 && $1) || (sleep 10 && $1)
}

function get_subctl() {
    test -x /go/bin/subctl && return
    version=$(travis_retry get_latest_subctl_tag || echo v0.1.0)
    curl -L https://github.com/submariner-io/submariner-operator/releases/download/${version}/subctl-${version}-linux-amd64 \
         -o /go/bin/subctl
    chmod a+x /go/bin/subctl
}

function deploytool_prereqs() {
    get_subctl
}

function setup_broker() {
    context=$1
    echo Installing broker on $context.
    kubectl config use-context $context
    subctl --kubeconfig ${PRJ_ROOT}/output/kind-config/dapper/kind-config-$context deploy-broker --no-dataplane
}

function subctl_install_subm() {
    context=$1
    kubectl config use-context $context
    subctl join --kubeconfig ${PRJ_ROOT}/output/kind-config/dapper/kind-config-$context \
                --clusterid ${context} \
                --repository ${subm_engine_image_repo} \
                --version ${subm_engine_image_tag} \
                --nattport ${ce_ipsec_nattport} \
                --ikeport ${ce_ipsec_ikeport} \
                --colorcodes ${subm_colorcodes} \
                --disable-nat \
                broker-info.subm
}

function install_subm_all_clusters() {
    for i in 1 2 3; do
        context=cluster$i
        subctl_install_subm $context
    done
}

function deploytool_postreqs() {
    # FIXME: Make this unnecessary using subctl v0.0.4 --no-label flag
    # subctl wants a gateway node labeled, or it will ask, but this script is not interactive,
    # and E2E expects cluster1 to not have the gateway configured at start, so we remove it
    del_subm_gateway_label cluster1
    # Just removing the label does not stop Subm pod.
    kubectl --context=cluster1 delete pod -n submariner-operator -l app=submariner-engine
}
