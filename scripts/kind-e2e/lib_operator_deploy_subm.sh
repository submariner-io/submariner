#!/bin/bash
# This should only be sourced
if [ "${0##*/}" = "lib_operator_deploy_subm.sh" ]; then
    echo "Don't run me, source me" >&2
    exit 1
fi

openapi_checks_enabled=false
subm_op_dir=$(realpath ./kind-e2e/operator)

function create_resource_if_missing() {
  resource_type=$1
  resource_name=$2
  resource_yaml=$3
  if ! kubectl get --namespace=$subm_ns $resource_type $resource_name; then
    kubectl create --namespace=$subm_ns -f $resource_yaml
  fi
}

function add_subm_gateway_label() {
  kubectl label node $context-worker "submariner.io/gateway=true" --overwrite
}

function create_subm_clusters_crd() {
  pushd $subm_op_dir

  clusters_crd_file=submariner_clusters_crd.yaml

  # TODO: Can/should we create this with Op-SDK?
cat <<EOF > $clusters_crd_file
apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
metadata:
  name: clusters.submariner.io
spec:
  group: submariner.io
  version: v1
  names:
    kind: Cluster
    plural: clusters
  scope: Namespaced
EOF

  cat $clusters_crd_file

  # Create clusters CRD
  # NB: This must be done before submariner-engine pod is deployed
  create_resource_if_missing crd clusters.submariner.io $clusters_crd_file

  popd
}

function create_subm_endpoints_crd() {
  pushd $subm_op_dir

  endpoints_crd_file=submariner_endpoints_crd.yaml

  # TODO: Can/should we create this with Op-SDK?
cat <<EOF > $endpoints_crd_file
apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
metadata:
  name: endpoints.submariner.io
  annotations:
spec:
  group: submariner.io
  version: v1
  names:
    kind: Endpoint
    plural: endpoints
  scope: Namespaced
EOF

  cat $endpoints_crd_file

  # Create endpoints CRD
  # NB: This must be done before submariner-engine pod is deployed
  create_resource_if_missing crd endpoints.submariner.io $endpoints_crd_file

  popd
}

function deploy_subm_operator() {
  pushd $subm_op_dir

  # If SubM namespace doesn't exist, create it
  if ! kubectl get ns $subm_ns; then
    # Customize namespace definition for subm_ns defined here
    cat namespace.yaml
    sed -i "s|submariner|$subm_ns|g" namespace.yaml
    cat namespace.yaml

    kubectl create -f namespace.yaml
  fi

  if ! kubectl get crds submariners.submariner.io; then
    kubectl create -f submariner.io_submariners_crd.yaml
  fi

  # Create SubM Operator service account if it doesn't exist
  create_resource_if_missing sa submariner-operator service_account.yaml

  # Create SubM Operator role if it doesn't exist
  create_resource_if_missing role submariner-operator role.yaml

  # Create SubM Operator role binding if it doesn't exist
  create_resource_if_missing rolebinding submariner-operator role_binding.yaml

  # Create SubM Operator deployment if it doesn't exist
  create_resource_if_missing deployment submariner-operator deploy-operator-local.yml

  # Wait for SubM Operator pod to be ready
  kubectl wait --for=condition=Ready pods -l name=submariner-operator --timeout=120s --namespace=$subm_ns

  popd
}

# FIXME: Call this submariner-engine vs submariner?
function create_subm_cr() {
  pushd $subm_op_dir

  cr_file_base=submariner.io_v1alpha1_submariner_cr.yaml
  cr_file=submariner-cr-$context.yaml

  # Create copy of default SubM CR (from operator-sdk)
  cp $cr_file_base $cr_file

  # Show base CR file
  cat $cr_file

  # Verify CR file exists
  [ -f $cr_fil_go ]

  # TODO: Use $engine_deployment_name here?
  sed -i "s|name: example-submariner|name: $deployment_name|g" $cr_file

  sed -i "/spec:/a \ \ size: $subm_engine_size" $cr_file

  # These all need to end up in pod container/environment vars
  sed -i "/spec:/a \ \ namespace: $subm_ns" $cr_file
  if [[ $context = cluster2 ]]; then
    sed -i "/spec:/a \ \ serviceCIDR: $serviceCIDR_cluster2" $cr_file
    sed -i "/spec:/a \ \ clusterCIDR: $clusterCIDR_cluster2" $cr_file
  elif [[ $context = cluster3 ]]; then
    sed -i "/spec:/a \ \ serviceCIDR: $serviceCIDR_cluster3" $cr_file
    sed -i "/spec:/a \ \ clusterCIDR: $clusterCIDR_cluster3" $cr_file
  fi
  sed -i "/spec:/a \ \ clusterID: $context" $cr_file
  sed -i "/spec:/a \ \ colorCodes: $subm_colorcodes" $cr_file
  # NB: Quoting bool-like vars is required or Go will type as bool and fail when set as env vars as strs
  sed -i "/spec:/a \ \ debug: $subm_debug" $cr_file
  # NB: Quoting bool-like vars is required or Go will type as bool and fail when set as env vars as strs
  sed -i "/spec:/a \ \ natEnabled: $natEnabled" $cr_file
  sed -i "/spec:/a \ \ broker: $subm_broker" $cr_file
  sed -i "/spec:/a \ \ brokerK8sApiServer: $SUBMARINER_BROKER_URL" $cr_file
  sed -i "/spec:/a \ \ brokerK8sApiServerToken: $SUBMARINER_BROKER_TOKEN" $cr_file
  sed -i "/spec:/a \ \ brokerK8sRemoteNamespace: $SUBMARINER_BROKER_NS" $cr_file
  sed -i "/spec:/a \ \ brokerK8sCA: $SUBMARINER_BROKER_CA" $cr_file
  sed -i "/spec:/a \ \ ceIPSecPSK: $SUBMARINER_PSK" $cr_file
  # NB: Quoting bool-like vars is required or Go will type as bool and fail when set as env vars as strs
  sed -i "/spec:/a \ \ ceIPSecDebug: $ce_ipsec_debug" $cr_file
  sed -i "/spec:/a \ \ ceIPSecIKEPort: $ce_ipsec_ikeport" $cr_file
  sed -i "/spec:/a \ \ ceIPSecNATTPort: $ce_ipsec_nattport" $cr_file
  sed -i "/spec:/a \ \ repository: $subm_engine_image_repo" $cr_file
  sed -i "/spec:/a \ \ version: $subm_engine_image_tag" $cr_file

  # Show completed CR file for debugging help
  cat $cr_file

  popd
}

function deploy_subm_cr() {
  pushd $subm_op_dir

  # FIXME: This must match cr_file value used in create_subm_cr fn
  cr_file=submariner-cr-$context.yaml

  # Create SubM CR if it doesn't exist
  if kubectl get submariner 2>&1 | grep -q "No resources found"; then
    kubectl apply --namespace=$subm_ns -f $cr_file
  fi

  popd
}

function deploy_netshoot_cluster2() {
    kubectl config use-context cluster2
    echo Deploying netshoot on cluster2 worker: ${worker_ip}
    kubectl apply -f ./kind-e2e/netshoot.yaml
    echo Waiting for netshoot pods to be Ready on cluster2.
    kubectl rollout status deploy/netshoot --timeout=120s

    # TODO: Add verifications
}

function deploy_nginx_cluster3() {
    kubectl config use-context cluster3
    echo Deploying nginx on cluster3 worker: ${worker_ip}
    kubectl apply -f ./kind-e2e/nginx-demo.yaml
    echo Waiting for nginx-demo deployment to be Ready on cluster3.
    kubectl rollout status deploy/nginx-demo --timeout=120s

    # TODO: Add verifications
    # TODO: Do this with nginx operator?
}
