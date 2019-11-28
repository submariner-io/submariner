#!/bin/bash
# This should only be sourced
if [ "${0##*/}" = "lib_operator_deploy_subm.sh" ]; then
    echo "Don't run me, source me" >&2
    exit 1
fi

function get_subctl() {
    test -x /go/bin/subctl && return
    curl -L https://github.com/submariner-io/submariner-operator/releases/download/v0.0.2/subctl-v0.0.2-linux-amd64 \
         -o /go/bin/subctl
    chmod a+x /go/bin/subctl
}

function add_subm_gateway_label() {
  kubectl label node $context-worker "submariner.io/gateway=true" --overwrite
}

function del_subm_gateway_label() {
  kubectl label node $context-worker "submariner.io/gateway-" --overwrite
}

function deploy_netshoot_cluster2() {
    kubectl config use-context cluster2
    echo Deploying netshoot on cluster2 worker: ${worker_ip}
    kubectl apply -f ./kind-e2e/netshoot.yaml
    echo Waiting for netshoot pods to be Ready on cluster2.
    kubectl rollout status deploy/netshoot --timeout=120s

    # TODO: Add verifications
}

function deploy_nginx_cluster3() {
    kubectl config use-context cluster3
    echo Deploying nginx on cluster3 worker: ${worker_ip}
    kubectl apply -f ./kind-e2e/nginx-demo.yaml
    echo Waiting for nginx-demo deployment to be Ready on cluster3.
    kubectl rollout status deploy/nginx-demo --timeout=120s

    # TODO: Add verifications
    # TODO: Do this with nginx operator?
}

get_subctl