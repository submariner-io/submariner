module github.com/submariner-io/submariner

go 1.12

require (
	github.com/bronze1man/goStrongswanVici v0.0.0-20190921045355-4c81bd8d0bd5
	github.com/coreos/go-iptables v0.4.5
	github.com/evanphx/json-patch v4.5.0+incompatible // indirect
	github.com/golang/groupcache v0.0.0-20200121045136-8c9f03a8e57e // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo v1.12.0
	github.com/onsi/gomega v1.9.0
	github.com/pkg/errors v0.9.1
	github.com/rdegges/go-ipify v0.0.0-20150526035502-2d94a6a86c40
	github.com/submariner-io/shipyard v0.0.0-20200324112155-1429f74326da
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/sys v0.0.0-20200202164722-d101bd2416d5
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20200324154536-ceff61240acf
	k8s.io/api v0.0.0-20190222213804-5cb15d344471
	k8s.io/apimachinery v0.0.0-20190629003722-e20a3a656cff
	k8s.io/client-go v0.0.0-20190521190702-177766529176
	k8s.io/klog v0.0.0-20181108234604-8139d8cb77af
	sigs.k8s.io/controller-runtime v0.1.12
)

replace github.com/onsi/ginkgo => github.com/onsi/ginkgo v0.0.0-20191002161935-034fd2551d22
