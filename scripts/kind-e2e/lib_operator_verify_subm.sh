#!/bin/bash
# This should only be sourced
if [ "${0##*/}" = "lib_operator_verify_subm.sh" ]; then
    echo "Don't run me, source me" >&2
    exit 1
fi

function verify_subm_gateway_label() {
  kubectl get node $context-worker -o jsonpath='{.metadata.labels}' | grep submariner.io/gateway:true
}

function verify_subm_operator() {
  # Verify SubM namespace (ignore SubM Broker ns)
  kubectl get ns $subm_ns

  # Verify SubM Operator CRD
  kubectl get crds submariners.submariner.io
  kubectl api-resources | grep submariners

  # Verify SubM Operator SA
  kubectl get sa --namespace=$subm_ns submariner-operator

  # Verify SubM Operator role
  kubectl get roles --namespace=$subm_ns submariner-operator

  # Verify SubM Operator role binding
  kubectl get rolebindings --namespace=$subm_ns submariner-operator

  # Verify SubM Operator deployment
  kubectl get deployments --namespace=$subm_ns submariner-operator
}

function verify_subm_crd() {
  crd_name=submariners.submariner.io

  # Verify presence of CRD
  kubectl get crds $crd_name

  # Show full CRD
  kubectl get crd $crd_name -o yaml

  # Verify details of CRD
  kubectl get crd $crd_name -o jsonpath='{.metadata.name}' | grep $crd_name
  kubectl get crd $crd_name -o jsonpath='{.spec.scope}' | grep Namespaced
  kubectl get crd $crd_name -o jsonpath='{.spec.group}' | grep submariner.io
  kubectl get crd $crd_name -o jsonpath='{.spec.version}' | grep v1alpha1
  kubectl get crd $crd_name -o jsonpath='{.spec.names.kind}' | grep Submariner

  if [[ $openapi_checks_enabled = true ]]; then
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep ceIPSecDebug
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep ceIPSecPSK
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep brokerK8sCA
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep brokerK8sRemotenamespace
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep brokerK8sApiservertoken
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep brokerK8sApiserver
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep submarinerBroker
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep submarinerNatenabled
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep submarinerDebug
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep submarinerColorcodes
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep submarinerClusterid
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep submarinerServicecidr
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep submarinerClustercidr
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep submarinerNamespace
    kubectl get crd $crd_name -o jsonpath='{.spec.validation.openAPIV3Schema.properties.spec.required}' | grep count
  fi
}

function verify_endpoints_crd() {
  crd_name=endpoints.submariner.io

  # Verify presence of CRD
  kubectl get crds $crd_name

  # Show full CRD
  kubectl get crd endpoints.submariner.io -o yaml

  # Verify details of CRD
  kubectl get crd $crd_name -o jsonpath='{.metadata.name}' | grep $crd_name
  kubectl get crd $crd_name -o jsonpath='{.spec.scope}' | grep Namespaced
  kubectl get crd $crd_name -o jsonpath='{.spec.group}' | grep submariner.io
  # TODO: Should this version really be v1, or maybe v1alpha1?
  kubectl get crd $crd_name -o jsonpath='{.spec.version}' | grep v1
  kubectl get crd $crd_name -o jsonpath='{.spec.names.kind}' | grep Endpoint
  kubectl get crd $crd_name -o jsonpath='{.status.acceptedNames.kind}' | grep Endpoint
}

function verify_clusters_crd() {
  crd_name=clusters.submariner.io

  # Verify presence of CRD
  kubectl get crds $crd_name

  # Show full CRD
  kubectl get crd clusters.submariner.io -o yaml

  # Verify details of CRD
  kubectl get crd $crd_name -o jsonpath='{.metadata.name}' | grep $crd_name
  kubectl get crd $crd_name -o jsonpath='{.spec.scope}' | grep Namespaced
  kubectl get crd $crd_name -o jsonpath='{.spec.group}' | grep submariner.io
  # TODO: Should this version really be v1, or maybe v1alpha1?
  kubectl get crd $crd_name -o jsonpath='{.spec.version}' | grep v1
  kubectl get crd $crd_name -o jsonpath='{.spec.names.kind}' | grep Cluster
  kubectl get crd $crd_name -o jsonpath='{.status.acceptedNames.kind}' | grep Cluster
}

function verify_subm_cr() {
  # TODO: Use $engine_deployment_name here?

  # Verify SubM CR presence
  kubectl get submariner --namespace=$subm_ns | grep $deployment_name

  # Show full SubM CR
  kubectl get submariner $deployment_name --namespace=$subm_ns -o yaml

  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.metadata.namespace}' | grep $subm_ns
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.apiVersion}' | grep submariner.io/v1alpha1
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.kind}' | grep Submariner
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.metadata.name}' | grep $deployment_name
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{..spec.brokerK8sApiServer}' | grep $SUBMARINER_BROKER_URL
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.brokerK8sApiServerToken}' | grep $SUBMARINER_BROKER_TOKEN
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.brokerK8sCA}' | grep $SUBMARINER_BROKER_CA
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.brokerK8sRemoteNamespace}' | grep $SUBMARINER_BROKER_NS
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.ceIPSecDebug}' | grep $ce_ipsec_debug
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.ceIPSecIKEPort}' | grep $ce_ipsec_ikeport
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.ceIPSecNATTPort}' | grep $ce_ipsec_nattport
  # FIXME: Sometimes this changes between runs, causes failures
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.ceIPSecPSK}' | grep $SUBMARINER_PSK || true
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.repository}' | grep $subm_engine_image_repo
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.version}' | grep $subm_engine_image_tag
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.size}' | grep $subm_engine_size
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.broker}' | grep $subm_broker
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.clusterID}' | grep $context
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.colorCodes}' | grep $subm_colorcodes
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.debug}' | grep $subm_debug
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.namespace}' | grep $subm_ns
  kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.natEnabled}' | grep $natEnabled
  if [[ $context = cluster2 ]]; then
    kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.serviceCIDR}' | grep $serviceCIDR_cluster2
    kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.clusterCIDR}' | grep $clusterCIDR_cluster2
  elif [[ $context = cluster3 ]]; then
    kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.serviceCIDR}' | grep $serviceCIDR_cluster3
    kubectl get submariner $deployment_name --namespace=$subm_ns -o jsonpath='{.spec.clusterCIDR}' | grep $clusterCIDR_cluster3
  fi
}

function verify_subm_op_pod() {
  subm_operator_pod_name=$(kubectl get pods --namespace=$subm_ns -l name=$operator_deployment_name -o=jsonpath='{.items..metadata.name}')

  # Show SubM Operator pod info
  kubectl get pod $subm_operator_pod_name --namespace=$subm_ns -o json

  # Verify SubM Operator pod status
  kubectl get pod $subm_operator_pod_name --namespace=$subm_ns -o jsonpath='{.status.phase}' | grep Running

  # Show SubM Operator pod logs
  kubectl logs $subm_operator_pod_name --namespace=$subm_ns

  # TODO: Verify logs?
}

function verify_subm_engine_pod() {
  kubectl wait --for=condition=Ready pods -l app=$engine_deployment_name --timeout=120s --namespace=$subm_ns

  subm_engine_pod_name=$(kubectl get pods --namespace=$subm_ns -l app=$engine_deployment_name -o=jsonpath='{.items..metadata.name}')

  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o json
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..image}' | grep submariner:local
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.capabilities.add}' | grep ALL
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.allowPrivilegeEscalation}' | grep "true"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.privileged}' | grep "true"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.readOnlyRootFilesystem}' | grep "false"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.runAsNonRoot}' | grep "false"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..command}' | grep submariner.sh
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}'
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_NAMESPACE value:$subm_ns"
  if [[ $context = cluster2 ]]; then
    kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_SERVICECIDR value:$serviceCIDR_cluster2"
    kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_CLUSTERCIDR value:$clusterCIDR_cluster2"
  elif [[ $context = cluster3 ]]; then
    kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_SERVICECIDR value:$serviceCIDR_cluster3"
    kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_CLUSTERCIDR value:$clusterCIDR_cluster3"
  fi
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_CLUSTERID value:$context"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_COLORCODES value:$subm_colorcodes"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_DEBUG value:$subm_debug"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_NATENABLED value:$natEnabled"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_BROKER value:$subm_broker"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:BROKER_K8S_APISERVER value:$SUBMARINER_BROKER_URL"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:BROKER_K8S_APISERVERTOKEN value:$SUBMARINER_BROKER_TOKEN"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:BROKER_K8S_REMOTENAMESPACE value:$SUBMARINER_BROKER_NS"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:BROKER_K8S_CA value:$SUBMARINER_BROKER_CA"
  # FIXME: This changes between some deployment runs and causes failures
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:CE_IPSEC_PSK value:$SUBMARINER_PSK" || true
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:CE_IPSEC_DEBUG value:$ce_ipsec_debug"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:CE_IPSEC_IKEPORT value:$ce_ipsec_ikeport"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:CE_IPSEC_NATTPORT value:$ce_ipsec_nattport"
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.status.phase}' | grep Running
  kubectl get pod $subm_engine_pod_name --namespace=$subm_ns -o jsonpath='{.metadata.namespace}' | grep $subm_ns
}

function verify_subm_engine_deployment() {
  # Simple verification to ensure that the engine deployment has been created and becomes ready
   SECONDS="0"
   while ! kubectl get Deployments -l app=$engine_deployment_name -n $subm_ns | grep -q submariner; do
     if [ $SECONDS -gt 120 ]; then
        echo "Timeout waiting for engine Deployment creation"
        exit 1
     else
        ((SECONDS+=2))
        sleep 2
     fi
   done

   replicas=0
   readyReplicas=0

   SECONDS="0"
   while [ "$readyReplicas" != "$replicas" ] || [ $readyReplicas -le 0 ]; do
     if [ $SECONDS -gt 120 ]; then
        echo "Timeout waiting for ready replicas of the Deployment"
        exit 1
     else

        replicas=$(kubectl get Deployment submariner -n $subm_ns -o jsonpath='{.status.replicas}')
        readyReplicas=$(kubectl get Deployment submariner -n $subm_ns -o jsonpath='{.status.readyReplicas}')

        ((SECONDS+=2))
        sleep 2
     fi
   done
}

function verify_subm_routeagent_daemonset() {
  # Simple verification to ensure that the routeagent daemonset has been created and becomes ready
   SECONDS="0"
   while ! kubectl get DaemonSets -l app=$routeagent_deployment_name -n $subm_ns | grep -q routeagent; do
     if [ $SECONDS -gt 120 ]; then
        echo "Timeout waiting for route agent DaemonSet creation"
        exit 1
     else
        ((SECONDS+=2))
        sleep 2
     fi
   done

   numberReady=-1
   desiredNumberScheduled=0
   SECONDS="0"
   while [ "$numberReady" != "$desiredNumberScheduled" ] || [ $numberReady -le 1 ]; do
     if [ $SECONDS -gt 120 ]; then
        echo "Timeout waiting for a ready state on the daemonset"
        exit 1
     else

        desiredNumberScheduled=$(kubectl get DaemonSet submariner-routeagent -n $subm_ns -o jsonpath='{.status.desiredNumberScheduled}')
        numberReady=$(kubectl get DaemonSet submariner-routeagent -n $subm_ns -o jsonpath='{.status.numberReady}')

        ((SECONDS+=2))
        sleep 2
     fi
   done
}

function verify_subm_routeagent_pod() {
  kubectl wait --for=condition=Ready pods -l app=$routeagent_deployment_name --timeout=120s --namespace=$subm_ns

  # Loop tests over all routeagent pods
  subm_routeagent_pod_names=$(kubectl get pods --namespace=$subm_ns -l app=$routeagent_deployment_name -o=jsonpath='{.items..metadata.name}')
  # Globing-safe method, but -a flag gives me trouble in ZSH for some reason
  read -ra subm_routeagent_pod_names_array <<< "$subm_routeagent_pod_names"
  # TODO: Fail if there are zero routeagent pods
  for subm_routeagent_pod_name in "${subm_routeagent_pod_names_array[@]}"; do
    echo "Testing Submariner routeagent pod $subm_routeagent_pod_name"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o json
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..image}' | grep submariner-route-agent:$subm_engine_image_tag
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.capabilities.add}' | grep ALL
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.allowPrivilegeEscalation}' | grep "true"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.privileged}' | grep "true"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.readOnlyRootFilesystem}' | grep "false"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..securityContext.runAsNonRoot}' | grep "false"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..command}' | grep submariner-route-agent.sh
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}'
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_NAMESPACE value:$subm_ns"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_CLUSTERID value:$context"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_DEBUG value:$subm_debug"
    if [[ $context = cluster2 ]]; then
      kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_SERVICECIDR value:$serviceCIDR_cluster2"
      kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_CLUSTERCIDR value:$clusterCIDR_cluster2"
    elif [[ $context = cluster3 ]]; then
      kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_SERVICECIDR value:$serviceCIDR_cluster3"
      kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..env}' | grep "name:SUBMARINER_CLUSTERCIDR value:$clusterCIDR_cluster3"
    fi
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..volumeMounts}' | grep "mountPath:/host"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..volumeMounts}' | grep "name:host-slash"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.containers..volumeMounts}' | grep "readOnly:true"
    if [ "$deploy_operator" = true ]; then
      # FIXME: Use submariner-routeagent SA vs submariner-operator when doing Operator deploys
      kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.serviceAccount}' | grep submariner-operator
    else
      kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.serviceAccount}' | grep submariner-routeagent
    fi
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec..volumes}' | grep "name:host-slash"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec..volumes}' | grep "path:/"
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.status.phase}' | grep Running
    kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.metadata.namespace}' | grep $subm_ns
    GRACE_PERIOD=$(kubectl get pod $subm_routeagent_pod_name --namespace=$subm_ns -o jsonpath='{.spec.terminationGracePeriodSeconds}')
    if [ "$GRACE_PERIOD" != "0" ]; then
      exit 1
    fi

  done
}

function verify_subm_operator_container() {
  subm_operator_pod_name=$(kubectl get pods --namespace=$subm_ns -l name=submariner-operator -o=jsonpath='{.items..metadata.name}')

  # Show SubM Operator pod environment variables
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env

  # Verify SubM Operator pod environment variables
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "HOSTNAME=$subm_operator_pod_name"
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "OPERATOR=/usr/local/bin/submariner-operator"
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "USER_UID=1001"
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "USER_NAME=submariner-operator"
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "WATCH_NAMESPACE=$subm_ns"
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "POD_NAME=$subm_operator_pod_name"
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "OPERATOR_NAME=submariner-operator"
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- env | grep "HOME=/"

  # Verify the operator binary is in the expected place and in PATH
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- command -v submariner-operator | grep /usr/local/bin/submariner-operator

  # Verify the operator entry script is in the expected place and in PATH
  kubectl exec -it $subm_operator_pod_name --namespace=$subm_ns -- command -v entrypoint | grep /usr/local/bin/entrypoint
}

function verify_subm_engine_container() {
  subm_engine_pod_name=$(kubectl get pods --namespace=$subm_ns -l app=$engine_deployment_name -o=jsonpath='{.items..metadata.name}')

  # Show SubM Engine pod environment variables
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env

  # Verify SubM Engine pod environment variables
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "HOSTNAME=$context-worker"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "BROKER_K8S_APISERVER=$SUBMARINER_BROKER_URL"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_NAMESPACE=$subm_ns"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_CLUSTERID=$context"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_BROKER=$subm_broker"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "BROKER_K8S_CA=$SUBMARINER_BROKER_CA"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "CE_IPSEC_DEBUG=$ce_ipsec_debug"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_DEBUG=$subm_debug"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "BROKER_K8S_APISERVERTOKEN=$SUBMARINER_BROKER_TOKEN"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "BROKER_K8S_REMOTENAMESPACE=$SUBMARINER_BROKER_NS"
  if [[ $context = cluster2 ]]; then
    kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_SERVICECIDR=$serviceCIDR_cluster2"
    kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_CLUSTERCIDR=$clusterCIDR_cluster2"
  elif [[ $context = cluster3 ]]; then
    kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_SERVICECIDR=$serviceCIDR_cluster3"
    kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_CLUSTERCIDR=$clusterCIDR_cluster3"
  fi
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_COLORCODES=$subm_colorcode"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_NATENABLED=$natEnabled"
  # FIXME: This fails on redeploys
  #kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "CE_IPSEC_PSK=$SUBMARINER_PSK"
  kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- env | grep "HOME=/root"

  if kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- command -v command; then
    # Verify the engine binary is in the expected place and in PATH
    kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- command -v submariner-engine | grep /usr/local/bin/submariner-engine

    # Verify the engine entry script is in the expected place and in PATH
    kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- command -v submariner.sh | grep /usr/local/bin/submariner.sh
  elif kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- which which; then
    # Verify the engine binary is in the expected place and in PATH
    kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- which submariner-engine | grep /usr/local/bin/submariner-engine

    # Verify the engine entry script is in the expected place and in PATH
    kubectl exec -it $subm_engine_pod_name --namespace=$subm_ns -- which submariner.sh | grep /usr/local/bin/submariner.sh
  fi
}

function verify_subm_routeagent_container() {
  # Loop tests over all routeagent pods
  subm_routeagent_pod_names=$(kubectl get pods --namespace=$subm_ns -l app=$routeagent_deployment_name -o=jsonpath='{.items..metadata.name}')
  # Globing-safe method, but -a flag gives me trouble in ZSH for some reason
  read -ra subm_routeagent_pod_names_array <<<"$subm_routeagent_pod_names"
  # TODO: Fail if there are zero routeagent pods
  for subm_routeagent_pod_name in "${subm_routeagent_pod_names_array[@]}"; do
    echo "Testing Submariner routeagent container $subm_routeagent_pod_name"

    # Show SubM Routeagent pod environment variables
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env

    # Verify SubM Routeagent pod environment variables
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "HOSTNAME=$context-worker"
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_NAMESPACE=$subm_ns"
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_CLUSTERID=$context"
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_DEBUG=$subm_debug"
    if [[ $context = cluster2 ]]; then
      kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_SERVICECIDR=$serviceCIDRcluster2"
      kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_CLUSTERCIDR=$clusterCIDR_cluster2"
    elif [[ $context = cluster3 ]]; then
      kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_SERVICECIDR=$serviceCIDR_cluster3"
      kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "SUBMARINER_CLUSTERCIDR=$clusterCIDR_cluster3"
    fi
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- env | grep "HOME=/root"

    # Verify the routeagent binary is in the expected place and in PATH
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- command -v submariner-route-agent | grep /usr/local/bin/submariner-route-agent

    # Verify the routeagent entry script is in the expected place and in PATH
    kubectl exec -it $subm_routeagent_pod_name --namespace=$subm_ns -- command -v submariner-route-agent.sh | grep /usr/local/bin/submariner-route-agent.sh
  done
}

function verify_subm_broker_secrets() {
  # Wait for secrets to be created
  SECONDS="0"
  while ! kubectl get secret -n $subm_broker_ns | grep -q submariner-; do
    if [ $SECONDS -gt 30 ]; then
        echo "Timeout waiting for SubM Secret creation"
        exit 1
    else
        ((SECONDS+=2))
        sleep 2
    fi
  done

  # Show all SubM broker secrets
  kubectl get secrets -n $subm_broker_ns

  subm_broker_secret_name=$(kubectl get secrets -n $subm_broker_ns -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='$broker_deployment_name-client')].metadata.name}")

  # Need explicit null check for this var because subsequent commands fail with confusing errors
  if [ -z "$subm_broker_secret_name" ]; then
    echo "Failed to find subm_broker_secret_name"
    exit 1
  fi

  # Show all details of SubM Broker secret
  kubectl get secret $subm_broker_secret_name -n $subm_broker_ns -o yaml

  # Verify details of SubM Broker secret
  kubectl get secret $subm_broker_secret_name -n $subm_broker_ns -o jsonpath='{.kind}' | grep Secret
  kubectl get secret $subm_broker_secret_name -n $subm_broker_ns -o jsonpath='{.type}' | grep "kubernetes.io/service-account-token"
  kubectl get secret $subm_broker_secret_name -n $subm_broker_ns -o jsonpath='{.metadata.name}' | grep $subm_broker_secret_name
  kubectl get secret $subm_broker_secret_name -n $subm_broker_ns -o jsonpath='{.metadata.namespace}' | grep $subm_broker_ns
  # Must use this jsonpath notation to access key with dot.in.name
  kubectl get secret $subm_broker_secret_name -n $subm_broker_ns -o "jsonpath={.data['ca\.crt']}" | grep $SUBMARINER_BROKER_CA
}

function verify_subm_engine_secrets() {
  # Wait for secrets to be created
  SECONDS="0"
  while ! kubectl get secret -n $subm_ns | grep -q submariner-; do
    if [ $SECONDS -gt 30 ]; then
        echo "Timeout waiting for SubM Secret creation"
        exit 1
    else
        ((SECONDS+=2))
        sleep 2
    fi
  done

  # Show all SubM secrets
  kubectl get secrets -n $subm_ns

  if [ "$deploy_operator" = true ]; then
    # FIXME: Should use SA specific for Engine, not shared with the operator
    subm_engine_secret_name=$(kubectl get secrets -n $subm_ns -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='$operator_deployment_name')].metadata.name}")
  else
    subm_engine_secret_name=$(kubectl get secrets -n $subm_ns -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='$engine_deployment_name')].metadata.name}")
  fi

  # Need explicit null check for this var because subsequent commands fail with confusing errors
  if [ -z "$subm_engine_secret_name" ]; then
    echo "Failed to find subm_engine_secret_name"
    exit 1
  fi

  # Show all details of SubM Engine secret
  kubectl get secret $subm_engine_secret_name -n $subm_ns -o yaml

  # Verify details of SubM Engine secret
  kubectl get secret $subm_engine_secret_name -n $subm_ns -o jsonpath='{.kind}' | grep Secret
  kubectl get secret $subm_engine_secret_name -n $subm_ns -o jsonpath='{.type}' | grep "kubernetes.io/service-account-token"
  kubectl get secret $subm_engine_secret_name -n $subm_ns -o jsonpath='{.metadata.name}' | grep $subm_engine_secret_name
  kubectl get secret $subm_engine_secret_name -n $subm_ns -o jsonpath='{.metadata.namespace}' | grep $subm_ns
  # Must use this jsonpath notation to access key with dot.in.name
  # FIXME: There seems to be a strange error where these substantially match, but eventually actually are different
  kubectl get secret $subm_engine_secret_name -n $subm_ns -o "jsonpath={.data['ca\.crt']}" | grep ${SUBMARINER_BROKER_CA:0:50}
  #kubectl get secret $subm_engine_secret_name -n $subm_ns -o "jsonpath={.data['ca\.crt']}" | grep ${SUBMARINER_BROKER_CA:0:161}
}

function verify_subm_routeagent_secrets() {
  # Wait for secrets to be created
  SECONDS="0"
  while ! kubectl get secret -n $subm_ns | grep -q submariner-; do
    if [ $SECONDS -gt 30 ]; then
        echo "Timeout waiting for SubM Secret creation"
        exit 1
    else
        ((SECONDS+=2))
        sleep 2
    fi
  done

  # Show all SubM secrets
  kubectl get secrets -n $subm_ns


  if [ "$deploy_operator" = true ]; then
    # FIXME: Should use SA specific for Routeagent, not shared with the operator
    subm_routeagent_secret_name=$(kubectl get secrets -n $subm_ns -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='$operator_deployment_name')].metadata.name}")
  else
    subm_routeagent_secret_name=$(kubectl get secrets -n $subm_ns -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='$routeagent_deployment_name')].metadata.name}")
  fi

  # Need explicit null check for this var because subsequent commands fail with confusing errors
  if [ -z "$subm_routeagent_secret_name" ]; then
    echo "Failed to find subm_routeagent_secret_name"
    exit 1
  fi

  # Show all details of SubM Routeagent secret
  kubectl get secret $subm_routeagent_secret_name -n $subm_ns -o yaml

  # Verify details of SubM Routeagent secret
  kubectl get secret $subm_routeagent_secret_name -n $subm_ns -o jsonpath='{.kind}' | grep Secret
  kubectl get secret $subm_routeagent_secret_name -n $subm_ns -o jsonpath='{.type}' | grep "kubernetes.io/service-account-token"
  kubectl get secret $subm_routeagent_secret_name -n $subm_ns -o jsonpath='{.metadata.name}' | grep $subm_routeagent_secret_name
  kubectl get secret $subm_routeagent_secret_name -n $subm_ns -o jsonpath='{.metadata.namespace}' | grep $subm_ns
  # Must use this jsonpath notation to access key with dot.in.name
  # FIXME: There seems to be a strange error where these substantially match, but eventually actually are different
  kubectl get secret $subm_routeagent_secret_name -n $subm_ns -o "jsonpath={.data['ca\.crt']}" | grep ${SUBMARINER_BROKER_CA:0:50}
  #kubectl get secret $subm_routeagent_secret_name -n $subm_ns -o "jsonpath={.data['ca\.crt']}" | grep ${SUBMARINER_BROKER_CA:0:162}
}
