package ipam

import "time"

type Operation string

const (
	handlerResync          = time.Hour * 24
	submarinerIpamGlobalIp = "submariner.io/globalIp"
	submarinerGlobalNet    = "SUBMARINER-GLOBALNET"

	// Currently Submariner Globalnet implementation (for services) works with kube-proxy
	// and uses iptable chain-names programmed by kube-proxy. If the internal implementation
	// of kube-proxy changes, globalnet needs to be modified accordingly.
	// Reference: https://bit.ly/2OPhlwk
	kubeProxyServiceChainPrefix = "KUBE-SVC-"
	kubeProxyNameSpace          = "kube-system"
	kubeProxyLabelSelector      = "k8s-app=kube-proxy"

	AddRules    = true
	DeleteRules = false

	Process = "Process"
	Ignore  = "Ignore"
	Requeue = "Requeue"
)
