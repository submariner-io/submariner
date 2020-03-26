#!/usr/bin/env bash
set -em

source ${SCRIPTS_DIR}/lib/debug_functions
source ${SCRIPTS_DIR}/lib/version
source ${SCRIPTS_DIR}/lib/utils

### Variables ###

E2E_DIR=${DAPPER_SOURCE}/scripts/kind-e2e/

### Functions ###

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

function add_subm_gateway_label() {
    context=$1
    kubectl --context=$context label node $context-worker "submariner.io/gateway=true" --overwrite
}

function del_subm_gateway_label() {
    context=$1
    kubectl --context=$context label node $context-worker "submariner.io/gateway-" --overwrite
}

function prepare_cluster() {
    for app in submariner-engine submariner-routeagent submariner-globalnet; do
        if kubectl wait --for=condition=Ready pods -l app=$app -n $subm_ns --timeout=60s > /dev/null 2>&1; then
            echo Removing $app pods...
            kubectl delete pods -n $subm_ns -l app=$app
        fi
    done
    add_subm_gateway_label $cluster
}

function deploy_resource() {
    cluster=$1
    resource_file=$2
    resource_name=$(basename "$2" ".yaml")
    kubectl apply -f ${resource_file}
    echo Waiting for ${resource_name} pods to be ready.
    kubectl rollout status deploy/${resource_name} --timeout=120s
}

function test_with_e2e_tests {
    set -o pipefail 

    cd ../test/e2e

    go test -v -args -ginkgo.v -ginkgo.randomizeAllSpecs \
        -submariner-namespace $subm_ns -dp-context cluster2 -dp-context cluster3 -dp-context cluster1 \
        -ginkgo.noColor -ginkgo.reportPassed \
        -ginkgo.reportFile ${DAPPER_OUTPUT}/e2e-junit.xml 2>&1 | \
        tee ${DAPPER_OUTPUT}/e2e-tests.log
}

function registry_running() {
    docker ps --filter name="^/?$KIND_REGISTRY$" | grep $KIND_REGISTRY
    return $?
}

function delete_cluster() {
    if [[ $(kind get clusters | grep ${cluster} | wc -l) -gt 0  ]]; then
        kind delete cluster --name=${cluster};
    fi
}

function cleanup {
    run_parallel "{1..3}" delete_cluster

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

LONGOPTS=status:,logging:,kubefed:,deploytool:,globalnet:
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

echo Starting with status: $status, logging: $logging, kubefed: $kubefed, deploy: $deploy, globalnet: $globalnet

declare_cidrs
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

if [[ $logging = true ]]; then
    enable_logging
fi

kind_import_images

if [[ $kubefed = true ]]; then
    # FIXME: Kubefed deploys are broken (not because of this commit)
    enable_kubefed
fi

# Install Helm/Operator deploy tool prerequisites
deploytool_prereqs

run_parallel "{1..3}" prepare_cluster

setup_broker cluster1
install_subm_all_clusters

deploytool_postreqs

deploy_resource "cluster2" "${E2E_DIR}/netshoot.yaml"
deploy_resource "cluster3" "${E2E_DIR}/nginx-demo.yaml"

test_connection

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
