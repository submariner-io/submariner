#!/bin/bash
set -ex

# Work around https://github.com/operator-framework/operator-sdk/issues/1675
GOROOT="$(go env GOROOT)"
export GOROOT
export GO111MODULE=on
GOPATH=$HOME/go

# Rely on the Go proxy to accelerate downloads and avoid problems with
# disappearing repositories
export GOPROXY=https://proxy.golang.org

version=0.0.1
op_dir=$GOPATH/src/github.com/submariner-operator/submariner-operator
op_gen_dir=$(pwd)
op_out_dir=$op_gen_dir/submariner-operator

function setup_prereqs(){
  # NB: There must be a running K8s cluster pointed at by the exported KUBECONFIG
  # for operator-sdk to work (although this dependency doesn't make sense)
  kind delete cluster || true # make sure any pre-existing cluster is removed, otherwise it fails in dapper
  kind create cluster || true
  export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"
  kubectl config use-context kubernetes-admin@kind
}

function initialize_subm_operator() {
  mkdir -p $op_dir
  pushd $op_dir/..
  rm -rf $op_dir
  operator-sdk new submariner-operator --verbose
  popd

  pushd $op_dir
  cat deploy/operator.yaml
  sed -i "s|REPLACE_IMAGE|quay.io/submariner/submariner-operator:$version|g" deploy/operator.yaml
  cat deploy/operator.yaml

  # Add example SubM namespace definition
  cp $op_gen_dir/example_subm_ns.yaml deploy/namespace.yaml

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
  sed -i '/SubmarinerSpec struct/a \ \ Namespace string `json:"namespace"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ ClusterCIDR string `json:"clusterCIDR"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ ServiceCIDR string `json:"serviceCIDR"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ ClusterID string `json:"clusterID"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ ColorCodes string `json:"colorCodes,omitempty"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ Debug bool `json:"debug"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ NatEnabled bool `json:"natEnabled"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ Broker string `json:"broker"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ BrokerK8sApiServer string `json:"brokerK8sApiServer"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ BrokerK8sApiServerToken string `json:"brokerK8sApiServerToken"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ BrokerK8sRemoteNamespace string `json:"brokerK8sRemoteNamespace"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ BrokerK8sCA string `json:"brokerK8sCA"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ CeIPSecPSK string `json:"ceIPSecPSK"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ CeIPSecDebug bool `json:"ceIPSecDebug"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ CeIPSecIKEPort int `json:"ceIPSecIKEPort,omitempty"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ CeIPSecNATTPort int `json:"ceIPSecNATTPort,omitempty"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ Repository string `json:"repository,omitempty"`' $types_file
  sed -i '/SubmarinerSpec struct/a \ \ Version string `json:"version,omitempty"`' $types_file

  # Define status fields, commented example
  # sed -i '/SubmarinerStatus struct/a \ \ PodNames []string `json:"pod_names"`' $types_file

  # Fix formatting of types file
  go fmt $types_file

  # Show completed types file
  cat $types_file

  # Must rebuild after modifying types file
  operator-sdk generate k8s
  operator-sdk generate openapi

  operator-sdk add controller --api-version=$api_version --kind=$kind

  controller_file_src=$op_gen_dir/submariner_controller.go.nolint
  controller_file_dst=pkg/controller/submariner/submariner_controller.go
  cp $controller_file_src $controller_file_dst

  popd
}

function export_subm_op() {
  rm -rf $op_out_dir
  cp -a $op_dir/. $op_out_dir/
}

# Make sure prereqs are installed
setup_prereqs

# Create SubM Operator
initialize_subm_operator
add_subm_engine_to_operator

export_subm_op
