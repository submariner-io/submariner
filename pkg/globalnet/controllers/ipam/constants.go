package ipam

import "time"

const handlerResync = time.Hour * 24
const submarinerIpamGlobalIp = "submariner.io/globalIp"
const submarinerGlobalNet = "SUBMARINER-GLOBALNET"
const kubeProxyServiceChainPrefix = "KUBE-SVC-"
const kubeProxyNameSpace = "kube-system"
const kubeProxyLabelSelector = "k8s-app=kube-proxy"

const AddRules = "Add"
const DeleteRules = "Delete"

const Process = "Process"
const Ignore = "Ignore"
const Requeue = "Requeue"

type Operation string
