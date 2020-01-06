#!/bin/bash
# This should only be sourced
if [ "${0##*/}" = "lib_operator_deploy_subm.sh" ]; then
    echo "Don't run me, source me" >&2
    exit 1
fi

function create_subm_vars() {
    # FIXME A better name might be submariner-engine, but just kinda-matching submariner-<random hash> name used by Helm/upstream tests
    deployment_name=submariner
    operator_deployment_name=submariner-operator
    engine_deployment_name=submariner-engine
    routeagent_deployment_name=submariner-routeagent
    broker_deployment_name=submariner-k8s-broker

    clusterCIDR_cluster1=10.244.0.0/16
    clusterCIDR_cluster2=10.245.0.0/16
    clusterCIDR_cluster3=10.246.0.0/16
    serviceCIDR_cluster2=100.95.0.0/16
    serviceCIDR_cluster3=100.96.0.0/16
    serviceCIDR_cluster1=100.94.0.0/16
    natEnabled=false

    subm_engine_image_repo=local
    subm_engine_image_tag=local

    # FIXME: Actually act on this size request in controller
    subm_engine_size=3
    subm_colorcodes=blue
    subm_debug=false
    subm_broker=k8s
    ce_ipsec_debug=false
    ce_ipsec_ikeport=500
    ce_ipsec_nattport=4500

    subm_ns=submariner-operator
    subm_broker_ns=submariner-k8s-broker
}

function preinstall_cleanup_subm {
    context=$1
    delete_subm_pods $context submariner-operator
}

function get_subctl() {
    test -x /go/bin/subctl && return
    curl -L https://github.com/submariner-io/submariner-operator/releases/download/v0.0.2/subctl-v0.0.2-linux-amd64 \
         -o /go/bin/subctl
    chmod a+x /go/bin/subctl
}

function deploytool_prereqs() {
    get_subctl
}

function setup_broker() {
    context=$1
    echo Installing broker on $context.
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
    # FIXME: Make this unncecesary using subctl v0.0.4 --no-label flag
    # subctl wants a gateway node labeled, or it will ask, but this script is not interactive,
    # and E2E expects cluster1 to not have the gateway configured at start, so we remove it
    del_subm_gateway_label cluster1
    # Just removing the label does not stop Subm pod.
    kubectl --context=cluster1 delete pod -n submariner-operator -l app=submariner-engine
}
