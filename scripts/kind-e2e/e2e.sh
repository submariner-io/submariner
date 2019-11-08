#!/usr/bin/env bash
set -em

source $(git rev-parse --show-toplevel)/scripts/lib/debug_functions

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
                kind create cluster --image=kindest/node:v${version} --name=cluster${i} --config=${PRJ_ROOT}/scripts/kind-e2e/cluster${i}-config.yaml
            else
                kind create cluster --name=cluster${i} --config=${PRJ_ROOT}/scripts/kind-e2e/cluster${i}-config.yaml
            fi
            master_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster${i}-control-plane | head -n 1)
            sed -i -- "s/user: kubernetes-admin/user: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")
            sed -i -- "s/name: kubernetes-admin.*/name: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")
            sed -i -- "s/current-context: kubernetes-admin.*/current-context: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")

            if [[ ${status} = keep ]]; then
                cp -r $(kind get kubeconfig-path --name="cluster$i") ${PRJ_ROOT}/output/kind-config/local-dev/kind-config-cluster${i}
            fi

            sed -i -- "s/server: .*/server: https:\/\/$master_ip:6443/g" $(kind get kubeconfig-path --name="cluster$i")
            cp -r $(kind get kubeconfig-path --name="cluster$i") ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster${i}
            ) > ${logs[$i]} 2>&1 &
            set pids[$i] = $!
        fi
    done
    if [[ ${#logs[@]} -gt 0 ]]; then
        echo "(Watch the installation processes with \"tail -f ${logs[*]}\".)"
        for i in 1 2 3; do
            if [[ pids[$i] -gt -1 ]]; then
                wait ${pids[$i]}
                if [[ $? -ne 0 && $? -ne 127 ]]; then
                    echo Cluster $i creation failed:
                    cat ${logs[$i]}
                fi
                rm -f ${logs[$i]}
            fi
        done
    fi
}

function install_helm() {
    helm init --client-only
    helm repo add submariner-latest https://submariner-io.github.io/submariner-charts/charts
    pids=(-1 -1 -1)
    logs=()
    for i in 1 2 3; do
        # Skip other clusters on operator deployment, we only need it on the first
        if [ "$i" != 1 ] && [ "$deploy_operator" = true ]; then
            echo "Skipping other clusters since we're deploying with operator."
            break
        fi

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
    if [[ ${#logs[@]} -gt 0 ]]; then
        echo "(Watch the installation processes with \"tail -f ${logs[*]}\".)"
        for i in 1 2 3; do
            if [[ pids[$i] -gt -1 ]]; then
                wait ${pids[$i]}
                if [[ $? -ne 0 && $? -ne 127 ]]; then
                    echo Cluster $i creation failed:
                    cat ${logs[$i]}
                fi
                rm -f ${logs[$i]}
            fi
        done
    fi
}

function setup_custom_cni(){
    declare -A POD_CIDR=( ["cluster2"]="10.245.0.0/16" ["cluster3"]="10.246.0.0/16" )
    for i in 2 3; do
        if kubectl --context=cluster${i} wait --for=condition=Ready pods -l name=weave-net -n kube-system --timeout=60s > /dev/null 2>&1; then
            echo "Weave already deployed cluster${i}."
        else
            echo "Applying weave network in to cluster${i}..."
            kubectl --context=cluster${i} apply -f "https://cloud.weave.works/k8s/net?k8s-version=$(kubectl version | base64 | tr -d '\n')&env.IPALLOC_RANGE=${POD_CIDR[cluster${i}]}"
            echo "Waiting for weave-net pods to be ready cluster${i}..."
            kubectl --context=cluster${i} wait --for=condition=Ready pods -l name=weave-net -n kube-system --timeout=700s
            echo "Waiting for core-dns deployment to be ready cluster${i}..."
            kubectl --context=cluster${i} -n kube-system rollout status deploy/coredns --timeout=300s
        fi
    done
}

function setup_broker() {
    if kubectl --context=cluster1 get crd clusters.submariner.io > /dev/null 2>&1; then
        echo Submariner CRDs already exist, skipping broker creation...
    else
        echo Installing broker on cluster1.
        helm --kube-context cluster1 install submariner-latest/submariner-k8s-broker --name ${SUBMARINER_BROKER_NS} --namespace ${SUBMARINER_BROKER_NS}
    fi

    SUBMARINER_BROKER_URL=$(kubectl --context=cluster1 -n default get endpoints kubernetes -o jsonpath="{.subsets[0].addresses[0].ip}:{.subsets[0].ports[?(@.name=='https')].port}")
    SUBMARINER_BROKER_CA=$(kubectl --context=cluster1 -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data['ca\.crt']}")
    SUBMARINER_BROKER_TOKEN=$(kubectl --context=cluster1 -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data.token}"|base64 --decode)
}

# Only install Submariner on broker cluster with 2 worker nodes but do not tag any worker nodes with gateway label
function install_subm_in_cluster1() {
    if kubectl --context=cluster1 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s > /dev/null 2>&1; then
            echo Submariner already installed, skipping submariner helm installation...
            update_subm_pods cluster1
        else
            echo Installing submariner on cluster1...
            helm --kube-context cluster1 install submariner-latest/submariner \
            --name submariner \
            --namespace submariner \
            --set ipsec.psk="${SUBMARINER_PSK}" \
            --set broker.server="${SUBMARINER_BROKER_URL}" \
            --set broker.token="${SUBMARINER_BROKER_TOKEN}" \
            --set broker.namespace="${SUBMARINER_BROKER_NS}" \
            --set broker.ca="${SUBMARINER_BROKER_CA}" \
            --set submariner.clusterId="cluster1" \
            --set submariner.clusterCidr="10.244.0.0/16" \
            --set submariner.serviceCidr="100.94.0.0/16" \
            --set submariner.natEnabled="false" \
            --set routeAgent.image.repository="submariner-route-agent" \
            --set routeAgent.image.tag="local" \
            --set routeAgent.image.pullPolicy="IfNotPresent" \
            --set engine.image.repository="submariner" \
            --set engine.image.tag="local" \
            --set engine.image.pullPolicy="IfNotPresent" \
            --set crd.create="false"
            echo Waiting for submariner pods to be Ready on cluster1...
            kubectl --context=cluster1 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s
            kubectl --context=cluster1 wait --for=condition=Ready pods -l app=submariner-routeagent -n submariner --timeout=60s
    fi
}

function setup_cluster2_gateway() {
    if kubectl --context=cluster2 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s > /dev/null 2>&1; then
            echo Submariner already installed, skipping submariner helm installation...
            update_subm_pods cluster2
        else
            echo Installing submariner on cluster2...
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
            --set engine.image.pullPolicy="IfNotPresent"
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
             --set engine.image.pullPolicy="IfNotPresent"
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
    OPERATOR_IMAGE=${OPERATOR_IMAGE:-quay.io/submariner/submariner-operator:0.0.1}
    docker tag quay.io/submariner/submariner:dev submariner:local
    docker tag quay.io/submariner/submariner-route-agent:dev submariner-route-agent:local

    if [[ "$deploy_operator" = true ]]; then

       # if the image is not local, first pull it from the remote docker registry
       if ! docker image inspect $OPERATOR_IMAGE 2>/dev/null ; then
          docker pull $OPERATOR_IMAGE
       fi
       docker tag $OPERATOR_IMAGE submariner-operator:local
    fi

    for i in 2 3; do
        echo "Loading submariner images in to cluster${i}..."
        kind --name cluster${i} load docker-image submariner:local
        kind --name cluster${i} load docker-image submariner-route-agent:local
        if [[ "$deploy_operator" = true ]]; then
             kind --name cluster${i} load docker-image submariner-operator:local
	fi
    done
}

function create_subm_vars() {
  # FIXME A better name might be submariner-engine, but just kinda-matching submariner-<random hash> name used by Helm/upstream tests
  deployment_name=submariner
  operator_deployment_name=submariner-operator
  engine_deployment_name=submariner-engine
  routeagent_deployment_name=submariner-routeagent
  broker_deployment_name=submariner-k8s-broker

  clusterCIDR_cluster2=10.245.0.0/16
  clusterCIDR_cluster3=10.246.0.0/16
  serviceCIDR_cluster2=100.95.0.0/16
  serviceCIDR_cluster3=100.96.0.0/16
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

  subm_ns=operators
  subm_broker_ns=submariner-k8s-broker
}

function test_connection() {
    nginx_svc_ip_cluster3=$(kubectl --context=cluster3 get svc -l app=nginx-demo | awk 'FNR == 2 {print $3}')
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
        for i in 2 3; do
            kubectl --context=cluster${i} apply -f ${PRJ_ROOT}/scripts/kind-e2e/logging/filebeat.yaml
            kubectl --context=cluster${i} set env daemonset/filebeat -n kube-system ELASTICSEARCH_HOST=${es_ip} ELASTICSEARCH_PORT=30000
        done
    fi
}

function enable_kubefed() {
    if kubectl --context=cluster1 rollout status deploy/kubefed-controller-manager -n ${KUBEFED_NS} > /dev/null 2>&1; then
        echo Kubefed already installed, skipping setup...
    else
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
        kubectl --context=cluster1 wait --for=condition=Ready pods -l control-plane=controller-manager -n ${KUBEFED_NS} --timeout=120s
        kubectl --context=cluster1 wait --for=condition=Ready pods -l kubefed-admission-webhook=true -n ${KUBEFED_NS} --timeout=120s
    fi
}

function test_with_e2e_tests {
    set -o pipefail 

    cd ../test/e2e

    # Setup the KUBECONFIG env
    export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')

    go test -v -args -ginkgo.v -ginkgo.randomizeAllSpecs \
        -dp-context cluster2 -dp-context cluster3 -dp-context cluster1 \
	      -ginkgo.noColor -ginkgo.reportPassed \
        -ginkgo.reportFile ${DAPPER_SOURCE}/${DAPPER_OUTPUT}/e2e-junit.xml 2>&1 | \
        tee ${DAPPER_SOURCE}/${DAPPER_OUTPUT}/e2e-tests.log
}

function cleanup {
    for i in 1 2 3; do
      if [[ $(kind get clusters | grep cluster${i} | wc -l) -gt 0  ]]; then
        kind delete cluster --name=cluster${i};
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

if [ "$5" = operator ]; then
   echo Deploying with operator
   deploy_operator=true
fi

echo Starting with status: $1, k8s_version: $2, logging: $3, kubefed: $4.
PRJ_ROOT=$(git rev-parse --show-toplevel)
mkdir -p ${PRJ_ROOT}/output/kind-config/dapper/ ${PRJ_ROOT}/output/kind-config/local-dev/
SUBMARINER_BROKER_NS=submariner-k8s-broker
# FIXME: This can change and break re-running deployments
SUBMARINER_PSK=$(cat /dev/urandom | LC_CTYPE=C tr -dc 'a-zA-Z0-9' | fold -w 64 | head -n 1)
KUBEFED_NS=kube-federation-system
export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')

kind_clusters "$@"
setup_custom_cni
if [[ $3 = true ]]; then
    enable_logging
fi

install_helm
if [[ $4 = true ]]; then
    enable_kubefed
fi

kind_import_images
setup_broker

context=cluster1
kubectl config use-context $context

# Import functions for testing with Operator
# NB: These are also used to verify non-Operator deployments, thereby asserting the two are mostly equivalent
. kind-e2e/lib_operator_verify_subm.sh

create_subm_vars
verify_subm_broker_secrets

if [ "$deploy_operator" = true ]; then
    . kind-e2e/lib_operator_deploy_subm.sh

    for i in 2 3; do
      context=cluster$i
      kubectl config use-context $context

      # Create CRDs required as prerequisite submariner-engine
      # TODO: Eventually OLM should handle this
      create_subm_endpoints_crd
      verify_endpoints_crd
      create_subm_clusters_crd
      verify_clusters_crd

      # Add SubM gateway labels
      add_subm_gateway_label
      # Verify SubM gateway labels
      verify_subm_gateway_label

      # Deploy SubM Operator
      deploy_subm_operator
      # Verify SubM CRD
      verify_subm_crd
      # Verify SubM Operator
      verify_subm_operator
      # Verify SubM Operator pod
      verify_subm_op_pod
      # Verify SubM Operator container
      verify_subm_operator_container

      # FIXME: Rename all of these submariner-engine or engine, vs submariner
      # Create SubM CR
      create_subm_cr
      # Deploy SubM CR
      deploy_subm_cr
      # Verify SubM CR
      verify_subm_cr
      # Verify SubM Engine Deployment
      verify_subm_engine_deployment
      # Verify SubM Engine Pod
      verify_subm_engine_pod
      # Verify SubM Engine container
      verify_subm_engine_container
      # Verify Engine secrets
      verify_subm_engine_secrets

      # Verify SubM Routeagent DaemonSet
      verify_subm_routeagent_daemonset
      # Verify SubM Routeagent Pods
      verify_subm_routeagent_pod
      # Verify SubM Routeagent container
      verify_subm_routeagent_container
      # Verify Routeagent secrets
      verify_subm_routeagent_secrets
    done

    deploy_netshoot_cluster2
    deploy_nginx_cluster3
elif [[ $5 = helm ]]; then
    helm=true
    install_subm_in_cluster1
    setup_cluster2_gateway
    setup_cluster3_gateway
    for i in 2 3; do
      context=cluster$i
      kubectl config use-context $context

      # The Helm deploy doesn't respect namespace config, hardcode to what it uses
      subm_ns=submariner

      verify_subm_engine_deployment
      verify_subm_engine_pod
      verify_subm_routeagent_daemonset
      verify_subm_routeagent_pod
      verify_subm_engine_container
      verify_subm_routeagent_container
      verify_subm_engine_secrets
      verify_subm_routeagent_secrets
    done
fi

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
    echo "to cleanup, just run: make ci e2e status=clean"
fi
