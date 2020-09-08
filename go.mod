module github.com/submariner-io/submariner

go 1.13

require (
	github.com/bronze1man/goStrongswanVici v0.0.0-20190921045355-4c81bd8d0bd5
	github.com/coreos/go-iptables v0.4.5
	github.com/go-logr/zapr v0.1.1 // indirect
	github.com/imdario/mergo v0.3.9 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/pkg/errors v0.9.1
	github.com/rdegges/go-ipify v0.0.0-20150526035502-2d94a6a86c40
	github.com/submariner-io/admiral v0.6.1
	github.com/submariner-io/shipyard v0.6.1
	github.com/vishvananda/netlink v1.1.0
	go.uber.org/zap v1.15.0 // indirect
	golang.org/x/sys v0.0.0-20200615200032-f1bc736245b1
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20200324154536-ceff61240acf
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.0
	k8s.io/client-go v0.17.0
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.3.0
	sigs.k8s.io/testing_frameworks v0.1.2 // indirect
)

// Pinned to kubernetes-1.17.0
replace (
	k8s.io/api => k8s.io/api v0.17.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.17.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.0
	k8s.io/client-go => k8s.io/client-go v0.17.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.17.0
)
