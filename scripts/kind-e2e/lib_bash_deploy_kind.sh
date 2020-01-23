#!/bin/bash
# This should only be sourced
if [ "${0##*/}" = "lib_bash_deploy_kind.sh" ]; then
    echo "Don't run me, source me" >&2
    exit 1
fi

### Variables ###

declare -A cluster_CIDRs=( ["cluster1"]="10.244.0.0/16" ["cluster2"]="10.245.0.0/16" ["cluster3"]="10.246.0.0/16" )
declare -A service_CIDRs=( ["cluster1"]="100.94.0.0/16" ["cluster2"]="100.95.0.0/16" ["cluster3"]="100.96.0.0/16" )

kubecfgs_rel_dir=output/kind-config/dapper
kubecfgs_dir=${DAPPER_SOURCE}/$kubecfgs_rel_dir

### Functions ###

function kind_clusters() {
    status=$1
    version=$2
    pids=(-1 -1 -1)
    logs=()
    for i in 1 2 3; do
        if [[ $(kind get clusters | grep cluster${i} | wc -l) -gt 0  ]]; then
            echo Cluster cluster${i} already exists, skipping cluster creation...
        else
            logs[$i]=$(mktemp)
            echo Creating cluster${i}, logging to ${logs[$i]}...
            (
            if [[ -n ${version} ]]; then
                kind create cluster --image=kindest/node:v${version} --name=cluster${i} --config=${DAPPER_SOURCE}/scripts/kind-e2e/cluster${i}-config.yaml
            else
                kind create cluster --name=cluster${i} --config=${DAPPER_SOURCE}/scripts/kind-e2e/cluster${i}-config.yaml
            fi
            master_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster${i}-control-plane | head -n 1)
            sed -i -- "s/user: kubernetes-admin/user: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")
            sed -i -- "s/name: kubernetes-admin.*/name: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")
            sed -i -- "s/current-context: kubernetes-admin.*/current-context: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")

            if [[ ${status} = keep ]]; then
                cp -r $(kind get kubeconfig-path --name="cluster$i") ${DAPPER_SOURCE}/output/kind-config/local-dev/kind-config-cluster${i}
            fi

            sed -i -- "s/server: .*/server: https:\/\/$master_ip:6443/g" $(kind get kubeconfig-path --name="cluster$i")
            cp -r $(kind get kubeconfig-path --name="cluster$i") ${DAPPER_SOURCE}/output/kind-config/dapper/kind-config-cluster${i}
            ) > ${logs[$i]} 2>&1 &
            set pids[$i] = $!
        fi
    done
    print_logs "${logs[@]}"
}

function import_subm_images() {
    docker tag quay.io/submariner/submariner:$VERSION submariner:local
    docker tag quay.io/submariner/submariner-route-agent:$VERSION submariner-route-agent:local

    for i in 1 2 3; do
        echo "Loading submariner images in to cluster${i}..."
        kind --name cluster${i} load docker-image submariner:local
        kind --name cluster${i} load docker-image submariner-route-agent:local
    done
}

function setup_custom_cni() {
    for i in 2 3; do
        if kubectl --context=cluster${i} wait --for=condition=Ready pods -l name=weave-net -n kube-system --timeout=60s > /dev/null 2>&1; then
            echo "Weave already deployed cluster${i}."
        else
            echo "Applying weave network in to cluster${i}..."
            kubectl --context=cluster${i} apply -f "https://cloud.weave.works/k8s/net?k8s-version=$(kubectl version | base64 | tr -d '\n')&env.IPALLOC_RANGE=${cluster_CIDRs[cluster${i}]}"
            echo "Waiting for weave-net pods to be ready cluster${i}..."
            kubectl --context=cluster${i} wait --for=condition=Ready pods -l name=weave-net -n kube-system --timeout=300s
            echo "Waiting for core-dns deployment to be ready cluster${i}..."
            kubectl --context=cluster${i} -n kube-system rollout status deploy/coredns --timeout=300s
        fi
    done
}

function create_kind_clusters() {
    kind_clusters "$@"
    setup_custom_cni
}

function destroy_kind_clusters() {
    for i in 1 2 3; do
      if [[ $(kind get clusters | grep cluster${i} | wc -l) -gt 0  ]]; then
        kind delete cluster --name=cluster${i};
      fi
    done
}
