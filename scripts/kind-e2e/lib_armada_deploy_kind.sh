#!/bin/bash
# This should only be sourced
if [ "${0##*/}" = "lib_armada_deploy_kind.sh" ]; then
    echo "Don't run me, source me" >&2
    exit 1
fi

### Variables ###

# FIXME: Use subctl info to collect these vs hard-coding
# https://github.com/submariner-io/submariner-operator/tree/master/pkg/discovery/network
declare -A cluster_CIDRs=( ["cluster1"]="10.4.0.0/14" ["cluster2"]="10.8.0.0/14" ["cluster3"]="10.12.0.0/14" )
declare -A service_CIDRs=( ["cluster1"]="100.1.0.0/16" ["cluster2"]="100.2.0.0/16" ["cluster3"]="100.3.0.0/16" )

kubecfgs_rel_dir=scripts/output/kube-config/container/
kubecfgs_dir=${DAPPER_SOURCE}/$kubecfgs_rel_dir

### Functions ###

function create_kind_clusters() {
    version=$2
    deploy=$3
    overlap_cidrs=$4

    # FIXME: Somehow don't leak helm/operator-specific logic into this lib
    if [[ $deploy = helm ]]; then
        deploy_flag="--tiller"
    fi

    if [[ $overlap_cidrs = true ]]; then
        overlap_flag="--overlap"
    fi

    /usr/bin/armada create clusters --image=kindest/node:v${version} -n 3 --weave $deploy_flag $overlap_flag
}

function import_subm_images() {
    docker tag quay.io/submariner/submariner:dev submariner:local
    docker tag quay.io/submariner/submariner-route-agent:dev submariner-route-agent:local

    /usr/bin/armada load docker-images --clusters cluster1,cluster2,cluster3 --images submariner:local,submariner-route-agent:local
}

function destroy_kind_clusters() {
    /usr/bin/armada destroy clusters
}
