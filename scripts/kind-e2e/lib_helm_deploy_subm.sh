#!/bin/bash
# This should only be sourced
if [ "${0##*/}" = "lib_helm_deploy_subm.sh" ]; then
    echo "Don't run me, source me" >&2
    exit 1
fi

### Variables ###

SUBMARINER_BROKER_NS=submariner-k8s-broker
SUBMARINER_PSK=$(cat /dev/urandom | LC_CTYPE=C tr -dc 'a-zA-Z0-9' | fold -w 64 | head -n 1)
subm_ns=submariner

### Functions ###

function install_helm() {
    helm init --client-only
    helm repo add submariner-latest https://submariner-io.github.io/submariner-charts/charts
    pids=(-1 -1 -1)
    logs=()
    for i in 1 2 3; do
        if kubectl --context=cluster${i} -n kube-system rollout status deploy/tiller-deploy > /dev/null 2>&1; then
            echo Helm already installed on cluster${i}, skipping helm installation...
        else
            logs[$i]=$(mktemp)
            echo Installing helm on cluster${i}, logging to ${logs[$i]}...
            (
            kubectl --context=cluster${i} -n kube-system create serviceaccount tiller
            kubectl --context=cluster${i} create clusterrolebinding tiller --clusterrole=cluster-admin --serviceaccount=kube-system:tiller
            helm --kube-context cluster${i} init --service-account tiller
            kubectl --context=cluster${i} -n kube-system rollout status deploy/tiller-deploy
            ) > ${logs[$i]} 2>&1 &
            set pids[$i] = $!
        fi
    done
    print_logs "${logs[@]}"
}

function deploytool_prereqs() {
    install_helm
}

function setup_broker() {
    context=$1
    if kubectl --context=$context get crd clusters.submariner.io > /dev/null 2>&1; then
        echo Submariner CRDs already exist, skipping broker creation...
    else
        echo Installing broker on $context.
        helm --kube-context $context install submariner-latest/submariner-k8s-broker --name ${SUBMARINER_BROKER_NS} --namespace ${SUBMARINER_BROKER_NS}
    fi

    SUBMARINER_BROKER_URL=$(kubectl --context=$context -n default get endpoints kubernetes -o jsonpath="{.subsets[0].addresses[0].ip}:{.subsets[0].ports[?(@.name=='https')].port}")
    SUBMARINER_BROKER_CA=$(kubectl --context=$context -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data['ca\.crt']}")
    SUBMARINER_BROKER_TOKEN=$(kubectl --context=$context -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data.token}"|base64 --decode)
}

function helm_install_subm() {
    cluster_id=$1
    cluster_cidr=$2
    service_cidr=$3
    crd_create=$4
    kubectl config use-context $cluster_id
    helm --kube-context ${cluster_id} install submariner-latest/submariner \
        --name submariner \
        --namespace submariner \
        --set ipsec.psk="${SUBMARINER_PSK}" \
        --set broker.server="${SUBMARINER_BROKER_URL}" \
        --set broker.token="${SUBMARINER_BROKER_TOKEN}" \
        --set broker.namespace="${SUBMARINER_BROKER_NS}" \
        --set broker.ca="${SUBMARINER_BROKER_CA}" \
        --set submariner.clusterId="${cluster_id}" \
        --set submariner.clusterCidr="${cluster_cidr}" \
        --set submariner.serviceCidr="${service_cidr}" \
        --set submariner.natEnabled="false" \
        --set routeAgent.image.repository="submariner-route-agent" \
        --set routeAgent.image.tag="local" \
        --set routeAgent.image.pullPolicy="IfNotPresent" \
        --set engine.image.repository="submariner" \
        --set engine.image.tag="local" \
        --set engine.image.pullPolicy="IfNotPresent" \
        --set crd.create="${crd_create}"
}


function install_subm_all_clusters() {
    helm_install_subm cluster1 ${cluster_CIDRs[cluster1]} ${service_CIDRs[cluster1]} false
    helm_install_subm cluster2 ${cluster_CIDRs[cluster2]} ${service_CIDRs[cluster2]} true
    helm_install_subm cluster3 ${cluster_CIDRs[cluster3]} ${service_CIDRs[cluster3]} true
}

function deploytool_postreqs() {
    # This function must exist for parity with Operator deploys, but does nothing for Helm
    :
}
