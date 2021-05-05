module github.com/submariner-io/submariner

go 1.13

require (
	github.com/cenk/hub v1.0.1 // indirect
	github.com/coreos/go-iptables v0.6.0
	github.com/ebay/go-ovn v0.1.1-0.20210414223409-7376ba97f8cd
	github.com/go-ping/ping v0.0.0-20210407214646-e4e642a95741
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo v1.16.2
	github.com/onsi/gomega v1.11.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.10.0
	github.com/rdegges/go-ipify v0.0.0-20150526035502-2d94a6a86c40
	github.com/submariner-io/admiral v0.9.0-rc0
	github.com/submariner-io/shipyard v0.9.0-rc0.0.20210428013206-a8065e7527c6
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/net v0.0.0-20210428140749-89ef3d95e781 // indirect
	golang.org/x/sys v0.0.0-20210426230700-d19ff857e887
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20210427135350-f9ad6d392236
	google.golang.org/protobuf v1.26.0
	k8s.io/api v0.18.4
	k8s.io/apimachinery v0.18.4
	k8s.io/client-go v0.18.4
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.6.1
	sigs.k8s.io/mcs-api v0.1.0
)

// Pinned to kubernetes-1.17.0
replace (
	k8s.io/api => k8s.io/api v0.17.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.17.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.17.0
	k8s.io/client-go => k8s.io/client-go v0.17.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.17.0
)

// Security fixes
replace (
	// CVE-2020-9283
	golang.org/x/crypto => golang.org/x/crypto v0.0.0-20210421170649-83a5a9bb288b
	// CVE-2020-14040
	golang.org/x/text => golang.org/x/text v0.3.6
)
