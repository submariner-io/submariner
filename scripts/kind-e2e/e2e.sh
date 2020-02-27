#!/usr/bin/env bash
set -em

source $(git rev-parse --show-toplevel)/scripts/lib/debug_functions
source $(git rev-parse --show-toplevel)/scripts/lib/version

### Variables ###

KIND_REGISTRY=kind-registry
E2E_DIR=$(dirname "$(readlink -f "$0")")

### Functions ###

function print_logs() {
    logs=("$@")
    if [[ ${#logs[@]} -eq 0 ]]; then
        return
    fi

    echo "(Watch the installation processes with \"tail -f ${logs[*]}\".)"
    for i in 1 2 3; do
        if [[ pids[$i] -eq -1 ]]; then
            continue
        fi

        wait ${pids[$i]}
        if [[ $? -ne 0 && $? -ne 127 ]]; then
            echo Cluster $i creation failed:
            cat ${logs[$i]}
        fi

        rm -f ${logs[$i]}
    done
}

function render_template() {
    eval "echo \"$(cat $1)\""
}

function generate_cluster_yaml() {
    pod_cidr="${cluster_CIDRs[$1]}"
    service_cidr="${service_CIDRs[$1]}"
    dns_domain="$1.local"
    disable_cni="true"
    if [[ "$1" = "cluster1" ]]; then
        disable_cni="false"
    fi

    render_template ${E2E_DIR}/kind-cluster-config.yaml > ${E2E_DIR}/$1-config.yaml
}

function kind_fixup_config() {
    cluster=$1
    master_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' ${cluster}-control-plane | head -n 1)
    sed -i -- "s/server: .*/server: https:\/\/$master_ip:6443/g" $KUBECONFIG
    sed -i -- "s/user: kind-.*/user: ${cluster}/g" $KUBECONFIG
    sed -i -- "s/name: kind-.*/name: ${cluster}/g" $KUBECONFIG
    sed -i -- "s/cluster: kind-.*/cluster: ${cluster}/g" $KUBECONFIG
    sed -i -- "s/current-context: .*/current-context: ${cluster}/g" $KUBECONFIG
    sudo chmod a+r $KUBECONFIG

    if [[ ${status} = keep ]]; then
        cp -r $KUBECONFIG ${PRJ_ROOT}/output/kind-config/local-dev/kind-config-${cluster}
    fi
}

function create_kind_clusters() {
    pids=(-1 -1 -1)
    logs=()
    for i in 1 2 3; do
        export KUBECONFIG=${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster${i}
        if [[ $(kind get clusters | grep "^cluster${i}$" | wc -l) -gt 0  ]]; then
            echo Cluster cluster${i} already exists, skipping cluster creation...
            kind export kubeconfig --name=cluster${i}
            kind_fixup_config cluster${i}
            continue
        fi

        logs[$i]=$(mktemp)
        echo Creating cluster${i}, logging to ${logs[$i]}...
        (
            generate_cluster_yaml "cluster${i}"
            image_flag=''
            if [[ -n ${version} ]]; then
                image_flag="--image=kindest/node:v${version}"
            fi

            kind create cluster $image_flag --name=cluster${i} --config=${E2E_DIR}/cluster${i}-config.yaml
            kind_fixup_config cluster${i}
        ) > ${logs[$i]} 2>&1 &
        set pids[$i] = $!
    done
    print_logs "${logs[@]}"
}

function use_kube_context() {
    kubectl config use-context $1
}

function setup_custom_cni(){
    for i in 2 3; do
        use_kube_context cluster$i
        if kubectl wait --for=condition=Ready pods -l name=weave-net -n kube-system --timeout=60s > /dev/null 2>&1; then
            echo "Weave already deployed cluster${i}."
            continue
        fi

        echo "Applying weave network in to cluster${i}..."
        kubectl apply -f "https://cloud.weave.works/k8s/net?k8s-version=$(kubectl version | base64 | tr -d '\n')&env.IPALLOC_RANGE=${cluster_CIDRs[cluster${i}]}"
        echo "Waiting for weave-net pods to be ready cluster${i}..."
        kubectl wait --for=condition=Ready pods -l name=weave-net -n kube-system --timeout=300s
        echo "Waiting for core-dns deployment to be ready cluster${i}..."
        kubectl -n kube-system rollout status deploy/coredns --timeout=300s
    done
}

function kind_import_images() {
    docker tag quay.io/submariner/submariner:$VERSION localhost:5000/submariner:local
    docker tag quay.io/submariner/submariner-route-agent:$VERSION localhost:5000/submariner-route-agent:local
    if [[ $globalnet = "true" ]]; then
        docker tag quay.io/submariner/submariner-globalnet:$VERSION localhost:5000/submariner-globalnet:local
    fi

    docker push localhost:5000/submariner:local
    docker push localhost:5000/submariner-route-agent:local
    if [[ $globalnet = "true" ]]; then
        docker push localhost:5000/submariner-globalnet:local
    fi
}

function get_globalip() {
    svcname=$1
    context=$2
    # It takes a while for globalIp annotation to show up on a service
    for i in {0..30}
    do
        gip=$(kubectl --context=$context get svc $svcname -o jsonpath='{.metadata.annotations.submariner\.io/globalIp}')
        if [[ -n ${gip} ]]; then
          echo $gip
          return
        fi
        sleep 1
    done
    echo "Max attempts reached, failed to get globalIp!"
    exit 1
}

function test_connection() {
    if [[ $globalnet = "true" ]]; then
        nginx_svc_ip_cluster3=$(get_globalip nginx-demo cluster3)
    else
        nginx_svc_ip_cluster3=$(kubectl --context=cluster3 get svc -l app=nginx-demo | awk 'FNR == 2 {print $3}')
    fi

    if [[ -z "$nginx_svc_ip_cluster3" ]]; then
        echo "Failed to get nginx-demo IP"
        exit 1
    fi
    netshoot_pod=$(kubectl --context=cluster2 get pods -l app=netshoot | awk 'FNR == 2 {print $1}')

    echo "Testing connectivity between clusters - $netshoot_pod cluster2 --> $nginx_svc_ip_cluster3 nginx service cluster3"

    attempt_counter=0
    max_attempts=5
    until $(kubectl --context=cluster2 exec ${netshoot_pod} -- curl --output /dev/null -m 30 --silent --head --fail ${nginx_svc_ip_cluster3}); do
        if [[ ${attempt_counter} -eq ${max_attempts} ]];then
          echo "Max attempts reached, connection test failed!"
          exit 1
        fi
        attempt_counter=$(($attempt_counter+1))
    done
    echo "Connection test was successful!"
}

function enable_logging() {
    use_kube_context cluster1

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
        use_kube_context cluster$i
        kubectl apply -f ${E2E_DIR}/logging/filebeat.yaml
        kubectl set env daemonset/filebeat -n kube-system ELASTICSEARCH_HOST=${es_ip} ELASTICSEARCH_PORT=30000
    done
}

function enable_kubefed() {
    use_kube_context cluster1
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

function add_subm_gateway_label() {
    context=$1
    kubectl --context=$context label node $context-worker "submariner.io/gateway=true" --overwrite
}

function del_subm_gateway_label() {
    context=$1
    kubectl --context=$context label node $context-worker "submariner.io/gateway-" --overwrite
}

function deploy_netshoot() {
    context=$1
    use_kube_context $context
    worker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $context-worker | head -n 1)
    echo Deploying netshoot on $context worker: ${worker_ip}
    kubectl apply -f ${E2E_DIR}/netshoot.yaml
    echo Waiting for netshoot pods to be Ready on $context.
    kubectl rollout status deploy/netshoot --timeout=120s
}

function deploy_nginx() {
    context=$1
    use_kube_context $context
    worker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $context-worker | head -n 1)
    echo Deploying nginx on $context worker: ${worker_ip}
    kubectl apply -f ${E2E_DIR}/nginx-demo.yaml
    echo Waiting for nginx-demo deployment to be Ready on $context.
    kubectl rollout status deploy/nginx-demo --timeout=120s
}

function test_with_e2e_tests {
    set -o pipefail 

    cd ../test/e2e

    # Setup the KUBECONFIG env
    export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')

    go test -v -args -ginkgo.v -ginkgo.randomizeAllSpecs \
        -submariner-namespace $subm_ns -dp-context cluster2 -dp-context cluster3 -dp-context cluster1 \
        -ginkgo.noColor -ginkgo.reportPassed \
        -ginkgo.reportFile ${DAPPER_SOURCE}/${DAPPER_OUTPUT}/e2e-junit.xml 2>&1 | \
        tee ${DAPPER_SOURCE}/${DAPPER_OUTPUT}/e2e-tests.log
}

function delete_subm_pods() {
    use_kube_context $1
    ns=$2
    app=$3
    if kubectl wait --for=condition=Ready pods -l app=$app -n $ns --timeout=60s > /dev/null 2>&1; then
        echo Removing $app pods...
        kubectl delete pods -n $ns -l app=$app
    fi
}

function registry_running() {
    docker ps --filter name="^/?$KIND_REGISTRY$" | grep $KIND_REGISTRY
    return $?
}

function cleanup {
    for i in 1 2 3; do
      if [[ $(kind get clusters | grep cluster${i} | wc -l) -gt 0  ]]; then
        kind delete cluster --name=cluster${i};
      fi
    done

    echo Removing local KIND registry...
    if registry_running; then
        docker stop $KIND_REGISTRY
    fi

    if [[ $(docker ps -qf status=exited | wc -l) -gt 0 ]]; then
        echo Cleaning containers...
        docker ps -qf status=exited | xargs docker rm -f
    fi
    if [[ $(docker images -qf dangling=true | wc -l) -gt 0 ]]; then
        echo Cleaning images...
        docker images -qf dangling=true | xargs docker rmi -f
    fi
#    if [[ $(docker images -q --filter=reference='submariner*:local' | wc -l) -gt 0 ]]; then
#        docker images -q --filter=reference='submariner*:local' | xargs docker rmi -f
#    fi
    if [[ $(docker volume ls -qf dangling=true | wc -l) -gt 0 ]]; then
        echo Cleaning volumes...
        docker volume ls -qf dangling=true | xargs docker volume rm -f
    fi
}

### Main ###

LONGOPTS=status:,k8s_version:,logging:,kubefed:,deploytool:,globalnet:
# Only accept longopts, but must pass null shortopts or first param after "--" will be incorrectly used
SHORTOPTS=""
! PARSED=$(getopt --options=$SHORTOPTS --longoptions=$LONGOPTS --name "$0" -- "$@")
eval set -- "$PARSED"

while true; do
    case "$1" in
        --status)
            status="$2"
            ;;
        --k8s_version)
            version="$2"
            ;;
        --logging)
            logging="$2"
            ;;
        --kubefed)
            kubefed="$2"
            ;;
        --deploytool)
            deploy="$2"
            ;;
        --globalnet)
            globalnet="$2"
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

echo Starting with status: $status, k8s_version: $version, logging: $logging, kubefed: $kubefed, deploy: $deploy, globalnet: $globalnet

if [[ $globalnet = "true" ]]; then
  # When globalnet is set to true, we want to deploy clusters with overlapping CIDRs
  declare -A cluster_CIDRs=( ["cluster1"]="10.244.0.0/16" ["cluster2"]="10.244.0.0/16" ["cluster3"]="10.244.0.0/16" )
  declare -A service_CIDRs=( ["cluster1"]="100.94.0.0/16" ["cluster2"]="100.94.0.0/16" ["cluster3"]="100.94.0.0/16" )
  declare -A global_CIDRs=( ["cluster1"]="169.254.1.0/24" ["cluster2"]="169.254.2.0/24" ["cluster3"]="169.254.3.0/24" )
else
  declare -A cluster_CIDRs=( ["cluster1"]="10.244.0.0/16" ["cluster2"]="10.245.0.0/16" ["cluster3"]="10.246.0.0/16" )
  declare -A service_CIDRs=( ["cluster1"]="100.94.0.0/16" ["cluster2"]="100.95.0.0/16" ["cluster3"]="100.96.0.0/16" )
fi

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

if [[ $deploy = operator ]]; then
    echo Will deploy submariner using the operator
    . ${E2E_DIR}/lib_operator_deploy_subm.sh
elif [ "$deploy" = helm ]; then
    echo Will deploy submariner using helm
    . ${E2E_DIR}/lib_helm_deploy_subm.sh
else
    echo Unknown deploy method: $deploy
    cleanup
    exit 1
fi

PRJ_ROOT=$(git rev-parse --show-toplevel)
rm -rf ${PRJ_ROOT}/output/kind-config/dapper/ ${PRJ_ROOT}/output/kind-config/local-dev/
mkdir -p ${PRJ_ROOT}/output/kind-config/dapper/ ${PRJ_ROOT}/output/kind-config/local-dev/
export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')

if [[ $logging = true ]]; then
    enable_logging
fi

# Run a local registry to avoid loading images manually to kind
if registry_running; then
    echo Local registry $KIND_REGISTRY already running.
else
    echo Deploying local registry $KIND_REGISTRY to serve images centrally.
    docker run -d -p 5000:5000 --restart=always --name $KIND_REGISTRY registry:2
fi
registry_ip="$(docker inspect -f '{{.NetworkSettings.IPAddress}}' "$KIND_REGISTRY")"

create_kind_clusters
export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')
setup_custom_cni
kind_import_images

if [[ $kubefed = true ]]; then
    # FIXME: Kubefed deploys are broken (not because of this commit)
    enable_kubefed
fi

# Install Helm/Operator deploy tool prerequisites
deploytool_prereqs

for i in 1 2 3; do
    context=cluster$i
    for app in submariner-engine submariner-routeagent submariner-globalnet; do
      delete_subm_pods $context $subm_ns $app
    done
    add_subm_gateway_label $context
done

setup_broker cluster1
install_subm_all_clusters

deploytool_postreqs

deploy_netshoot cluster2
deploy_nginx cluster3

test_connection

if [[ $status = keep || $status = onetime ]]; then
    test_with_e2e_tests
fi

if [[ $status = keep || $status = create ]]; then
    echo "your 3 virtual clusters are deployed and working properly with your local"
    echo "submariner source code, and can be accessed with:"
    echo ""
    echo "export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kind-config/local-dev/kind-config-cluster{1..3} | sed 's/ /:/g')"
    echo ""
    echo "$ kubectl config use-context cluster1 # or cluster2, cluster3.."
    echo ""
    echo "to cleanup, just run: make e2e status=clean"
fi
