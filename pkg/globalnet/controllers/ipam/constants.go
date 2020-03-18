package ipam

import "time"

type Operation string

const (
	handlerResync          = time.Hour * 24
	SubmarinerIpamGlobalIp = "submariner.io/globalIp"

	submarinerIngress = "SUBMARINER-GN-INGRESS"
	submarinerEgress  = "SUBMARINER-GN-EGRESS"
	submarinerMark    = "SUBMARINER-GN-MARK"

	// Globalnet uses MARK target to mark traffic destined to remote clusters.
	// Some of the CNIs also use iptable MARK targets in the pipeline. This should not
	// be a problem because Globalnet is only marking traffic destined to Submariner
	// connected clusters where Submariner takes full control on how the traffic is
	// steered in the pipeline. Normal traffic should not be affected because of this.
	globalNetIPTableMark = "0xC0000/0xC0000"

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
