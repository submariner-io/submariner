#!/usr/bin/env bash
set -em

source $(git rev-parse --show-toplevel)/scripts/lib/debug_functions

### Functions ###

function kind_clusters() {
    status=$1
    shift
    version=$1
    shift
    declare -A pids
    declare -A logs
    for cluster in "$@"; do
        if [[ $(kind get clusters | grep ${cluster} | wc -l) -gt 0  ]]; then
            echo Cluster ${cluster} already exists, skipping cluster creation...
        else
            logs[${cluster}]=$(mktemp)
            echo Creating ${cluster}, logging to ${logs[${cluster}]}...
            (
            if [[ -n ${version} ]]; then
                kind create cluster --image=kindest/node:v${version} --name=${cluster} --config=${PRJ_ROOT}/scripts/kind-e2e/${cluster}-config.yaml
            else
                kind create cluster --name=${cluster} --config=${PRJ_ROOT}/scripts/kind-e2e/${cluster}-config.yaml
            fi
            master_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' ${cluster}-control-plane | head -n 1)
            sed -i -- "s/user: kubernetes-admin/user: ${cluster}/g" $(kind get kubeconfig-path --name="${cluster}")
            sed -i -- "s/name: kubernetes-admin.*/name: ${cluster}/g" $(kind get kubeconfig-path --name="${cluster}")
            sed -i -- "s/current-context: kubernetes-admin.*/current-context: ${cluster}/g" $(kind get kubeconfig-path --name="${cluster}")

            if [[ ${status} = keep ]]; then
                cp -r $(kind get kubeconfig-path --name="${cluster}") ${PRJ_ROOT}/output/kind-config/local-dev/kind-config-${cluster}
            fi

            sed -i -- "s/server: .*/server: https:\/\/$master_ip:6443/g" $(kind get kubeconfig-path --name="${cluster}")
            cp -r $(kind get kubeconfig-path --name="${cluster}") ${PRJ_ROOT}/output/kind-config/dapper/kind-config-${cluster}
            ) > ${logs[${cluster}]} 2>&1 &
            set pids[${cluster}] = $!
        fi
    done
    if [[ ${#logs[@]} -gt 0 ]]; then
        echo "(Watch the installation processes with \"tail -f ${logs[*]}\".)"
        for cluster in "$@"; do
            if [[ pids[${cluster}] -gt -1 ]]; then
                wait ${pids[${cluster}]}
                if [[ $? -ne 0 && $? -ne 127 ]]; then
                    echo Cluster ${cluster} creation failed:
                    cat ${logs[${cluster}]}
                fi
                rm -f ${logs[${cluster}]}
            fi
        done
    fi
}

function install_helm() {
    helm init --client-only
    helm repo add submariner-latest https://submariner-io.github.io/submariner-charts/charts
    declare -A pids
    declare -A logs
    for cluster in "$@"; do
        if kubectl --context=${cluster} -n kube-system rollout status deploy/tiller-deploy > /dev/null 2>&1; then
            echo Helm already installed on ${cluster}, skipping helm installation...
        else
            logs[${cluster}]=$(mktemp)
            echo Installing helm on ${cluster}, logging to ${logs[${cluster}]}...
            (
            kubectl --context=${cluster} -n kube-system create serviceaccount tiller
            kubectl --context=${cluster} create clusterrolebinding tiller --clusterrole=cluster-admin --serviceaccount=kube-system:tiller
            helm --kube-context ${cluster} init --service-account tiller
            kubectl --context=${cluster} -n kube-system rollout status deploy/tiller-deploy
            ) > ${logs[${cluster}]} 2>&1 &
            set pids[${cluster}] = $!
        fi
    done
    if [[ ${#logs[@]} -gt 0 ]]; then
        echo "(Watch the installation processes with \"tail -f ${logs[*]}\".)"
        for cluster in "$@"; do
            if [[ pids[${cluster}] -gt -1 ]]; then
                wait ${pids[${cluster}]}
                if [[ $? -ne 0 && $? -ne 127 ]]; then
                    echo Helm installation in cluster ${cluster} failed:
                    cat ${logs[${cluster}]}
                fi
                rm -f ${logs[${cluster}]}
            fi
        done
    fi
}

function setup_custom_cni(){
    declare -A POD_CIDR=( ["cluster2"]="10.245.0.0/16" ["cluster3"]="10.246.0.0/16" )
    for cluster in "$@"; do
        if kubectl --context=${cluster} wait --for=condition=Ready pods -l name=weave-net -n kube-system --timeout=60s > /dev/null 2>&1; then
            echo "Weave already deployed ${cluster}."
        else
            echo "Applying weave network in to ${cluster}..."
            kubectl --context=${cluster} apply -f "https://cloud.weave.works/k8s/net?k8s-version=$(kubectl --context=${cluster} version | base64 | tr -d '\n')&env.IPALLOC_RANGE=${POD_CIDR[${cluster}]}"
            echo "Waiting for weave-net pods to be ready ${cluster}..."
            kubectl --context=${cluster} wait --for=condition=Ready pods -l name=weave-net -n kube-system --timeout=300s
            echo "Waiting for core-dns deployment to be ready ${cluster}..."
            kubectl --context=${cluster} -n kube-system rollout status deploy/coredns --timeout=300s
        fi
    done
}

function setup_broker() {
    if kubectl --context=$1 get crd clusters.submariner.io > /dev/null 2>&1; then
        echo Submariner CRDs already exist, skipping broker creation...
    else
        echo Installing broker on $1.
        helm --kube-context $1 install submariner-latest/submariner-k8s-broker --name ${SUBMARINER_BROKER_NS} --namespace ${SUBMARINER_BROKER_NS}
    fi

    SUBMARINER_BROKER_URL=$(kubectl --context=$1 -n default get endpoints kubernetes -o jsonpath="{.subsets[0].addresses[0].ip}:{.subsets[0].ports[?(@.name=='https')].port}")
    SUBMARINER_BROKER_CA=$(kubectl --context=$1 -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data['ca\.crt']}")
    SUBMARINER_BROKER_TOKEN=$(kubectl --context=$1 -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data.token}"|base64 --decode)
}

function setup_cluster2_gateway() {
    if kubectl --context=cluster2 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s > /dev/null 2>&1; then
            echo Submariner already installed, skipping submariner helm installation...
            update_subm_pods cluster2
        else
            echo Installing submariner on cluster2...
            crds=true
            if kubectl --context=cluster2 get crd clusters.submariner.io > /dev/null 2>&1; then
                crds=false
            fi
            worker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster2-worker | head -n 1)
            kubectl --context=cluster2 label node cluster2-worker "submariner.io/gateway=true" --overwrite
            helm --kube-context cluster2 install submariner-latest/submariner \
            --name submariner \
            --namespace submariner \
            --set ipsec.psk="${SUBMARINER_PSK}" \
            --set broker.server="${SUBMARINER_BROKER_URL}" \
            --set broker.token="${SUBMARINER_BROKER_TOKEN}" \
            --set broker.namespace="${SUBMARINER_BROKER_NS}" \
            --set broker.ca="${SUBMARINER_BROKER_CA}" \
            --set submariner.clusterId="cluster2" \
            --set submariner.clusterCidr="10.245.0.0/16" \
            --set submariner.serviceCidr="100.95.0.0/16" \
            --set submariner.natEnabled="false" \
            --set routeAgent.image.repository="submariner-route-agent" \
            --set routeAgent.image.tag="local" \
            --set routeAgent.image.pullPolicy="IfNotPresent" \
            --set engine.image.repository="submariner" \
            --set engine.image.tag="local" \
            --set engine.image.pullPolicy="IfNotPresent" \
            --set crd.create="${crds}"
            echo Waiting for submariner pods to be Ready on cluster2...
            kubectl --context=cluster2 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s
            kubectl --context=cluster2 wait --for=condition=Ready pods -l app=submariner-routeagent -n submariner --timeout=60s
            echo Deploying netshoot on cluster2 worker: ${worker_ip}
            kubectl --context=cluster2 apply -f ${PRJ_ROOT}/scripts/kind-e2e/netshoot.yaml
            echo Waiting for netshoot pods to be Ready on cluster2.
            kubectl --context=cluster2 rollout status deploy/netshoot --timeout=120s
    fi
}

function setup_cluster3_gateway() {
    if kubectl --context=cluster3 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s > /dev/null 2>&1; then
            echo Submariner already installed, skipping submariner helm installation...
            update_subm_pods cluster3
        else
            echo Installing submariner on cluster3...
            crds=true
            if kubectl --context=cluster3 get crd clusters.submariner.io > /dev/null 2>&1; then
                crds=false
            fi
            worker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster3-worker | head -n 1)
            kubectl --context=cluster3 label node cluster3-worker "submariner.io/gateway=true" --overwrite
            helm --kube-context cluster3 install submariner-latest/submariner \
             --name submariner \
             --namespace submariner \
             --set ipsec.psk="${SUBMARINER_PSK}" \
             --set broker.server="${SUBMARINER_BROKER_URL}" \
             --set broker.token="${SUBMARINER_BROKER_TOKEN}" \
             --set broker.namespace="${SUBMARINER_BROKER_NS}" \
             --set broker.ca="${SUBMARINER_BROKER_CA}" \
             --set submariner.clusterId="cluster3" \
             --set submariner.clusterCidr="10.246.0.0/16" \
             --set submariner.serviceCidr="100.96.0.0/16" \
             --set submariner.natEnabled="false" \
             --set routeAgent.image.repository="submariner-route-agent" \
             --set routeAgent.image.tag="local" \
             --set routeAgent.image.pullPolicy="IfNotPresent" \
             --set engine.image.repository="submariner" \
             --set engine.image.tag="local" \
             --set engine.image.pullPolicy="IfNotPresent" \
             --set crd.create="${crds}"
            echo Waiting for submariner pods to be Ready on cluster3...
            kubectl --context=cluster3 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s
            kubectl --context=cluster3 wait --for=condition=Ready pods -l app=submariner-routeagent -n submariner --timeout=60s
            echo Deploying nginx on cluster3 worker: ${worker_ip}
            kubectl --context=cluster3 apply -f ${PRJ_ROOT}/scripts/kind-e2e/nginx-demo.yaml
            echo Waiting for nginx-demo deployment to be Ready on cluster3.
            kubectl --context=cluster3 rollout status deploy/nginx-demo --timeout=120s
    fi
}

function kind_import_images() {
    docker tag rancher/submariner:dev submariner:local
    docker tag rancher/submariner-route-agent:dev submariner-route-agent:local

    for cluster in "$@"; do
        echo "Loading submariner images in to ${cluster}..."
        kind --name ${cluster} load docker-image submariner:local
        kind --name ${cluster} load docker-image submariner-route-agent:local
    done
}

function test_connection() {
    nginx_svc_ip_cluster3=$(kubectl --context=cluster3 get svc -l app=nginx-demo | awk 'FNR == 2 {print $3}')
    netshoot_pod=$(kubectl --context=cluster2 get pods -l app=netshoot | awk 'FNR == 2 {print $1}')

    echo "Testing connectivity between clusters - $netshoot_pod cluster2 --> $nginx_svc_ip_cluster3 nginx service cluster3"

    attempt_counter=0
    max_attempts=5
    until $(kubectl --context=cluster2 exec -it ${netshoot_pod} -- curl --output /dev/null -m 30 --silent --head --fail ${nginx_svc_ip_cluster3}); do
        if [[ ${attempt_counter} -eq ${max_attempts} ]];then
          echo "Max attempts reached, connection test failed!"
          exit 1
        fi
        attempt_counter=$(($attempt_counter+1))
    done
    echo "Connection test was successful!"
}

function update_subm_pods() {
    echo Removing submariner engine pods...
    kubectl --context=$1 delete pods -n submariner -l app=submariner-engine
    kubectl --context=$1 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s
    echo Removing submariner route agent pods...
    kubectl --context=$1 delete pods -n submariner -l app=submariner-routeagent
    kubectl --context=$1 wait --for=condition=Ready pods -l app=submariner-routeagent -n submariner --timeout=60s
}

function enable_logging() {
    if kubectl --context=cluster1 rollout status deploy/kibana > /dev/null 2>&1; then
        echo Elasticsearch stack already installed, skipping...
    else
        echo Installing Elasticsearch...
        es_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster1-control-plane | head -n 1)
        kubectl --context=cluster1 apply -f ${PRJ_ROOT}/scripts/kind-e2e/logging/elasticsearch.yaml
        kubectl --context=cluster1 apply -f ${PRJ_ROOT}/scripts/kind-e2e/logging/filebeat.yaml
        echo Waiting for Elasticsearch to be ready...
        kubectl --context=cluster1 wait --for=condition=Ready pods -l app=elasticsearch --timeout=300s
        for cluster in cluster{1,2}; do
            kubectl --context=${cluster} apply -f ${PRJ_ROOT}/scripts/kind-e2e/logging/filebeat.yaml
            kubectl --context=${cluster} set env daemonset/filebeat -n kube-system ELASTICSEARCH_HOST=${es_ip} ELASTICSEARCH_PORT=30000
        done
    fi
}

function enable_kubefed() {
    if kubectl --context=cluster1 rollout status deploy/kubefed-controller-manager -n ${KUBEFED_NS} > /dev/null 2>&1; then
        echo Kubefed already installed, skipping setup...
    else
        helm repo add kubefed-charts https://raw.githubusercontent.com/kubernetes-sigs/kubefed/master/charts
        helm --kube-context cluster1 install kubefed-charts/kubefed --version=0.1.0-rc2 --name kubefed --namespace ${KUBEFED_NS} --set controllermanager.replicaCount=1
        for cluster in cluster{1,2,3}; do
            kubefedctl join ${cluster} --cluster-context ${cluster} --host-cluster-context cluster1 --v=2
            #master_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' ${cluster}-control-plane | head -n 1)
            #kind_endpoint="https://${master_ip}:6443"
            #kubectl patch kubefedclusters -n ${KUBEFED_NS} ${cluster} --type merge --patch "{\"spec\":{\"apiEndpoint\":\"${kind_endpoint}\"}}"
        done
        #kubectl delete pod -l control-plane=controller-manager -n ${KUBEFED_NS}
        echo Waiting for kubefed control plain to be ready...
        kubectl --context=cluster1 wait --for=condition=Ready pods -l control-plane=controller-manager -n ${KUBEFED_NS} --timeout=120s
        kubectl --context=cluster1 wait --for=condition=Ready pods -l kubefed-admission-webhook=true -n ${KUBEFED_NS} --timeout=120s
    fi
}

function test_with_e2e_tests {
    set -o pipefail 

    cd ../test/e2e

    # Setup the KUBECONFIG env
    export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')

    go test -args -ginkgo.v -ginkgo.randomizeAllSpecs -ginkgo.reportPassed \
        -dp-context cluster2 -dp-context cluster3  \
        -report-dir ${DAPPER_SOURCE}/${DAPPER_OUTPUT}/junit 2>&1 | \
        tee ${DAPPER_SOURCE}/${DAPPER_OUTPUT}/e2e-tests.log
}

function cleanup {
    for cluster in cluster{1,2,3}; do
      if [[ $(kind get clusters | grep ${cluster} | wc -l) -gt 0  ]]; then
        kind delete cluster --name=${cluster};
      fi
    done

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

if [[ $1 = clean ]]; then
    cleanup
    exit 0
fi

if [[ $1 != keep ]]; then
    trap cleanup EXIT
fi

echo Starting with status: $1, k8s_version: $2, logging: $3, kubefed: $4, broker :$5.
PRJ_ROOT=$(git rev-parse --show-toplevel)
mkdir -p ${PRJ_ROOT}/output/kind-config/dapper/ ${PRJ_ROOT}/output/kind-config/local-dev/
SUBMARINER_BROKER_NS=submariner-k8s-broker
SUBMARINER_PSK=$(cat /dev/urandom | LC_CTYPE=C tr -dc 'a-zA-Z0-9' | fold -w 64 | head -n 1)
KUBEFED_NS=kube-federation-system
export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')

broker_cluster=cluster1
if [[ $5 = cluster1 || $5 = cluster2 || $5 = cluster3 ]]; then
    echo Broker on $5
    broker_cluster=$5
elif [[ -n $5 ]]; then
    echo Invalid broker setting $5, ignoring
fi

client_clusters=(cluster2 cluster3)

if [[ $broker_cluster = cluster2 || $broker_cluster = cluster3 ]]; then
    clusters=(cluster2 cluster3)
else
    clusters=(cluster1 cluster2 cluster3)
fi

kind_clusters "$1" "$2" "${clusters[@]}"
setup_custom_cni "${client_clusters[@]}"
if [[ $3 = true ]]; then
    enable_logging
fi
install_helm "${clusters[@]}"
if [[ $4 = true ]]; then
    enable_kubefed
fi
kind_import_images "${client_clusters[@]}"
setup_broker $broker_cluster
setup_cluster2_gateway
setup_cluster3_gateway
test_connection
test_with_e2e_tests

if [[ $1 = keep ]]; then
    echo "your 3 virtual clusters are deployed and working properly with your local"
    echo "submariner source code, and can be accessed with:"
    echo ""
    echo "export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kind-config/local-dev/kind-config-cluster{1..3} | sed 's/ /:/g')"
    echo ""
    echo "$ kubectl config use-context cluster1 # or cluster2, cluster3.."
    echo ""
    echo "to cleanup, just run: make e2e status=clean"
fi
