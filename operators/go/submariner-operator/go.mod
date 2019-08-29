module github.com/submariner-operator/submariner-operator

require (
	github.com/coreos/prometheus-operator v0.31.1 // indirect
	github.com/operator-framework/operator-sdk v0.10.1-0.20191007233534-070d931e130a
	github.com/spf13/pflag v1.0.3
	k8s.io/api v0.0.0-20190918155943-95b840bb6a1f
	k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
	k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	k8s.io/kube-openapi v0.0.0-20190603182131-db7b694dc208 // indirect
	sigs.k8s.io/controller-runtime v0.2.0
)

// Pinned to kubernetes-1.14.1
replace (
	k8s.io/api => k8s.io/api v0.0.0-20190409021203-6e4e0e4f393b
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20190409022649-727a075fdec8
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190404173353-6a84e37a896d
	k8s.io/client-go => k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.0.0-20190409023720-1bc0c81fa51d
)

replace (
	github.com/coreos/prometheus-operator => github.com/coreos/prometheus-operator v0.31.1
	// Pinned to v2.10.0 (kubernetes-1.14.1) so https://proxy.golang.org can
	// resolve it correctly.
	github.com/prometheus/prometheus => github.com/prometheus/prometheus v1.8.2-0.20190525122359-d20e84d0fb64
)
