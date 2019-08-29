#!/bin/bash
set -ex

# Work around https://github.com/operator-framework/operator-sdk/issues/1675
GOROOT="$(go env GOROOT)"
export GOROOT
export GO111MODULE=on
GOPATH=$HOME/go

version=0.0.1
add_engine=true
add_routeagent=true
openapi_checks_enabled=false
push_image=false
op_dir=$GOPATH/src/github.com/submariner-operator/submariner-operator
op_gen_dir=$GOPATH/src/github.com/submariner-io/submariner/operators/go
op_out_dir=$GOPATH/src/github.com/submariner-io/submariner/operators/go/submariner-operator

function setup_prereqs(){
  if ! command -v dep; then
    # Install dep
    curl https://raw.githubusercontent.com/golang/dep/master/install.sh | sh

    # Make sure go/bin is in path
    command -v dep
  fi

  # NB: There must be a running K8s cluster pointed at by the exported KUBECONFIG
  # for operator-sdk to work (although this dependency doesn't make sense)
  kind create cluster || true
  export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"
  kubectl config use-context kubernetes-admin@kind
}

function initilize_subm_operator() {
  mkdir -p $op_dir
  pushd $op_dir/..
  rm -rf $op_dir
  operator-sdk new submariner-operator --verbose
  popd

  pushd $op_dir
  cat deploy/operator.yaml
  sed -i "s|REPLACE_IMAGE|quay.io/submariner/submariner-operator:$version|g" deploy/operator.yaml
  cat deploy/operator.yaml

  # Create a definition namespace for SubM
  ns_file=deploy/namespace.yaml
cat <<EOF > $ns_file
{
  "apiVersion": "v1",
  "kind": "Namespace",
  "metadata": {
    "name": "submariner",
    "labels": {
      "name": "submariner"
    }
  }
}
EOF
  popd
}

function add_subm_engine_to_operator() {
  pushd $op_dir
  api_version=submariner.io/v1alpha1
  kind=Submariner
  operator-sdk add api --api-version=$api_version --kind=$kind

  # Define spec fields
  types_file=pkg/apis/submariner/v1alpha1/submariner_types.go
  sed -i '/SubmarinerSpec struct/a \ \ Count int32 `json:"count"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerNamespace string `json:"submariner_namespace"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerClustercidr string `json:"submariner_clustercidr"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerServicecidr string `json:"submariner_servicecidr"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerToken string `json:"submariner_token"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerClusterid string `json:"submariner_clusterid"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerColorcodes string `json:"submariner_colorcodes"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerDebug string `json:"submariner_debug"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerNatenabled string `json:"submariner_natenabled"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ SubmarinerBroker string `json:"submariner_broker"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ BrokerK8sApiserver string `json:"broker_k8s_apiserver"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ BrokerK8sApiservertoken string `json:"broker_k8s_apiservertoken"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ BrokerK8sRemotenamespace string `json:"broker_k8s_remotenamespace"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ BrokerK8sCa string `json:"broker_k8s_ca"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ CeIpsecPsk string `json:"ce_ipsec_psk"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ CeIpsecDebug string `json:"ce_ipsec_debug"`' $types_file

  # Define status fields
  # TODO: Is this needed/right or legacy?
  sed -i '/SubmarinerStatus struct/a \ \ PodNames []string `json:"pod_names"`' $types_file

  # Fix formatting of types file
  go fmt $types_file

  # Show completed types file
  cat $types_file

  # Must rebuild after modifying types file
  operator-sdk generate k8s
  if [[ $openapi_checks_enabled = true ]]; then
    operator-sdk generate openapi
  else
    operator-sdk generate openapi || true
  fi

  operator-sdk add controller --api-version=$api_version --kind=$kind

  controller_file_src=$op_gen_dir/submariner_controller.go
  controller_file_dst=pkg/controller/submariner/submariner_controller.go
  cp $controller_file_src $controller_file_dst

  popd
}

function add_subm_routeagent_to_operator() {
  pushd $op_dir
  api_version=submariner.io/v1alpha1
  kind=Routeagent
  operator-sdk add api --api-version=$api_version --kind=$kind || true

  # Define spec fields
  types_file=pkg/apis/submariner/v1alpha1/routeagent_types.go
  sed -i '/RouteagentSpec struct/a \ \ SubmarinerNamespace string `json:"submariner_namespace"`' $types_file
  sed -i '/RouteagentSpec struct/a \ \ SubmarinerClusterid string `json:"submariner_clusterid"`' $types_file
  sed -i '/RouteagentSpec struct/a \ \ SubmarinerDebug string `json:"submariner_debug"`' $types_file

  # Define status fields
  # TODO: Is this needed/right or legacy?
  sed -i '/SubmarinerStatus struct/a \ \ PodNames []string `json:"pod_names"`' $types_file

  # Fix formatting of types file
  go fmt $types_file

  # Show completed types file
  cat $types_file

  # Must rebuild after modifying types file
  operator-sdk generate k8s
  if [[ $openapi_checks_enabled = true ]]; then
    operator-sdk generate openapi
  else
    operator-sdk generate openapi || true
  fi

  operator-sdk add controller --api-version=$api_version --kind=$kind

  controller_file_src=$op_gen_dir/routeagent_controller.go
  controller_file_dst=pkg/controller/routeagent/routeagent_controller.go
  cp $controller_file_src $controller_file_dst

  popd
}

function build_subm_operator() {
  pushd $op_dir
  go mod vendor
  # This seems like a bug in operator-sdk, that this is needed?
  go get k8s.io/kube-state-metrics/pkg/collector
  go get k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1
  go get github.com/coreos/prometheus-operator/pkg/apis/monitoring

  operator-sdk build quay.io/submariner/submariner-operator:$version --verbose
  if [[ $push_image = true ]]; then
    docker push quay.io/submariner/submariner-operator:$version
  else
    echo "Skipping pushing SubM Operator image to Quay"
  fi

  popd
}

function export_subm_op() {
  rm -rf $op_out_dir
  cp -a $op_dir/. $op_out_dir/
}

# Make sure prereqs are installed
setup_prereqs

# Create SubM Operator
initilize_subm_operator
if [[ $add_engine = true ]]; then
  add_subm_engine_to_operator
fi
if [[ $add_routeagent = true ]]; then
  # WIP
  add_subm_routeagent_to_operator
fi
build_subm_operator

export_subm_op
