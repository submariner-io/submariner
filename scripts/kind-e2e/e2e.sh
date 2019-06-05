#!/usr/bin/env bash
set -e

SUBMARINER_BROKER_NS=submariner-k8s-broker
SUBMARINER_PSK=$(cat /dev/urandom | LC_CTYPE=C tr -dc 'a-zA-Z0-9' | fold -w 64 | head -n 1)

function kind_clusters() {
    for i in 1 2 3; do
        kind create cluster --name=cluster${i} --wait=5m --config=./kind-e2e/cluster${i}-config.yaml
        master_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster${i}-control-plane | head -n 1)
        sed -i -- "s/server: .*/server: https:\/\/$master_ip:6443/g" $(kind get kubeconfig-path --name="cluster$i")
        sed -i -- "s/user: kubernetes-admin/user: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")
        sed -i -- "s/name: kubernetes-admin.*/name: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")
    done

    export KUBECONFIG=$(kind get kubeconfig-path --name=cluster1):$(kind get kubeconfig-path --name=cluster2):$(kind get kubeconfig-path --name=cluster3)
}

function install_helm() {
    for i in 1 2 3; do
        kubectl config use-context cluster${i}
        kubectl -n kube-system create serviceaccount tiller
        kubectl create clusterrolebinding tiller --clusterrole=cluster-admin --serviceaccount=kube-system:tiller
        helm init --service-account tiller
        kubectl -n kube-system  rollout status deploy/tiller-deploy
    done
}

function setup_broker() {
    kubectl config use-context cluster1
    helm install submariner-latest/submariner-k8s-broker \
         --name ${SUBMARINER_BROKER_NS} \
         --namespace ${SUBMARINER_BROKER_NS}

    SUBMARINER_BROKER_URL=$(kubectl -n default get endpoints kubernetes -o jsonpath="{.subsets[0].addresses[0].ip}:{.subsets[0].ports[?(@.name=='https')].port}")
    SUBMARINER_BROKER_CA=$(kubectl -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data['ca\.crt']}")
    SUBMARINER_BROKER_TOKEN=$(kubectl -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data.token}"|base64 --decode)
}

function setup_cluster2_gateway() {
    kubectl config use-context cluster2
    worker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster2-worker | head -n 1)
    kubectl label node cluster2-worker "submariner.io/gateway=true" --overwrite
    helm install submariner-latest/submariner \
         --name submariner \
         --namespace submariner \
         --set ipsec.psk="${SUBMARINER_PSK}" \
         --set broker.server="${SUBMARINER_BROKER_URL}" \
         --set broker.token="${SUBMARINER_BROKER_TOKEN}" \
         --set broker.namespace="${SUBMARINER_BROKER_NS}" \
         --set broker.ca="${SUBMARINER_BROKER_CA}" \
         --set submariner.clusterId="cluster2" \
         --set submariner.clusterCidr="$worker_ip/32" \
         --set submariner.serviceCidr="100.95.0.0/16" \
         --set submariner.natEnabled="false" \
         --set routeAgent.image.repository="submariner-route-agent" \
         --set routeAgent.image.tag="local" \
         --set routeAgent.image.pullPolicy="IfNotPresent" \
         --set engine.image.repository="submariner" \
         --set engine.image.tag="local" \
         --set engine.image.pullPolicy="IfNotPresent"
    echo Waiting for submariner pods to be Ready on cluster2...
    kubectl wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s
    kubectl wait --for=condition=Ready pods -l app=submariner-routeagent -n submariner --timeout=60s
    echo Deploying netshoot on cluster2 worker: ${worker_ip}
    kubectl apply -f ./kind-e2e/netshoot.yaml
    echo Waiting for netshoot pods to be Ready on cluster2.
    kubectl rollout status deploy/netshoot --timeout=120s
}

function setup_cluster3_gateway() {
    kubectl config use-context cluster3
    worker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster3-worker | head -n 1)
    kubectl label node cluster3-worker "submariner.io/gateway=true" --overwrite
    helm install submariner-latest/submariner \
         --name submariner \
         --namespace submariner \
         --set ipsec.psk="${SUBMARINER_PSK}" \
         --set broker.server="${SUBMARINER_BROKER_URL}" \
         --set broker.token="${SUBMARINER_BROKER_TOKEN}" \
         --set broker.namespace="${SUBMARINER_BROKER_NS}" \
         --set broker.ca="${SUBMARINER_BROKER_CA}" \
         --set submariner.clusterId="cluster3" \
         --set submariner.clusterCidr="$worker_ip/32" \
         --set submariner.serviceCidr="100.96.0.0/16" \
         --set submariner.natEnabled="false" \
         --set routeAgent.image.repository="submariner-route-agent" \
         --set routeAgent.image.tag="local" \
         --set routeAgent.image.pullPolicy="IfNotPresent" \
         --set engine.image.repository="submariner" \
         --set engine.image.tag="local" \
         --set engine.image.pullPolicy="IfNotPresent"
    echo Waiting for submariner pods to be Ready on cluster3...
    kubectl wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s
    kubectl wait --for=condition=Ready pods -l app=submariner-routeagent -n submariner --timeout=60s
    echo Deploying nginx on cluster3 worker: ${worker_ip}
    kubectl apply -f ./kind-e2e/nginx-demo.yaml
    echo Waiting for nginx-demo deployment to be Ready on cluster3.
    kubectl rollout status deploy/nginx-demo --timeout=120s
}

function kind_import_images() {
    docker tag rancher/submariner:dev submariner:local
    docker tag rancher/submariner-route-agent:dev submariner-route-agent:local

    for i in 2 3; do
        echo "Loading submariner images in to cluster${i}..."
        kind --name cluster${i} load docker-image submariner:local
        kind --name cluster${i} load docker-image submariner-route-agent:local
    done
}

function test_connection() {
    kubectl config use-context cluster3
    nginx_svc_ip_cluster3=$(kubectl get svc -l app=nginx-demo | awk 'FNR == 2 {print $3}')
    kubectl config use-context cluster2
    netshoot_pod=$(kubectl get pods -l app=netshoot | awk 'FNR == 2 {print $1}')

    echo "Testing connectivity between clusters - $netshoot_pod cluster2 --> $nginx_svc_ip_cluster3 nginx service cluster3"
    for i in 1 2; do
        status_code=$(kubectl exec -it ${netshoot_pod} -- curl -m 30 -s -o /dev/null -w "%{http_code}" ${nginx_svc_ip_cluster3})
        if [[ $status_code -eq 200 ]]; then
           echo "Connection success! Status code: $status_code"
        fi
    done
}

function cleanup {
  echo "Cleanup..."
  for i in 1 2 3; do kind delete cluster --name=cluster${i}; done
}

trap cleanup EXIT

helm init --client-only
helm repo add submariner-latest https://releases.rancher.com/submariner-charts/latest

kind_clusters
kind_import_images
install_helm
setup_broker
setup_cluster2_gateway
setup_cluster3_gateway
test_connection
