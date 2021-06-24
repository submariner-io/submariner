module github.com/submariner-io/submariner

go 1.13

require (
	github.com/cenk/hub v1.0.1 // indirect
	github.com/coreos/go-iptables v0.6.0
	github.com/ebay/go-ovn v0.1.1-0.20210414223409-7376ba97f8cd
	github.com/emirpasic/gods v1.12.0
	github.com/go-ping/ping v0.0.0-20210407214646-e4e642a95741
	github.com/google/uuid v1.2.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/submariner-io/admiral v0.10.0-m2
	github.com/submariner-io/shipyard v0.10.0-m2.0.20210615173434-f15404d75718
	github.com/uw-labs/lichen v0.1.4
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/sys v0.0.0-20210603081109-ebe580a85c40
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20210427135350-f9ad6d392236
	google.golang.org/protobuf v1.27.0
	k8s.io/api v0.21.0
	k8s.io/apimachinery v0.21.0
	k8s.io/client-go v1.5.2
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.7.0
	sigs.k8s.io/mcs-api v0.1.0
)

// Pinned to kubernetes-1.19.10
replace (
	k8s.io/api => k8s.io/api v0.19.10
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.19.10
	k8s.io/apimachinery => k8s.io/apimachinery v0.19.10
	k8s.io/client-go => k8s.io/client-go v0.19.10
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.19.10
)

// Security fixes
replace (
	// CVE-2020-9283
	golang.org/x/crypto => golang.org/x/crypto v0.0.0-20210421170649-83a5a9bb288b
	// CVE-2020-14040
	golang.org/x/text => golang.org/x/text v0.3.6
)
