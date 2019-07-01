module github.com/rancher/submariner

go 1.12

require (
	github.com/bronze1man/goStrongswanVici v0.0.0-20181105005556-92d3927c899e
	github.com/coreos/go-iptables v0.4.0
	github.com/evanphx/json-patch v0.0.0-20180908160633-36442dbdb585 // indirect
	github.com/gogo/protobuf v0.0.0-20171007142547-342cbe0a0415 // indirect
	github.com/golang/groupcache v0.0.0-20160516000752-02826c3e7903 // indirect
	github.com/google/btree v0.0.0-20160524151835-7d79101e329e // indirect
	github.com/google/gofuzz v0.0.0-20161122191042-44d81051d367 // indirect
	github.com/googleapis/gnostic v0.0.0-20170729233727-0c5108395e2d // indirect
	github.com/gregjones/httpcache v0.0.0-20170728041850-787624de3eb7 // indirect
	github.com/hashicorp/golang-lru v0.0.0-20160207214719-a0d98a5f2880 // indirect
	github.com/imdario/mergo v0.0.0-20180608140156-9316a62528ac // indirect
	github.com/jpillora/backoff v0.0.0-20170918002102-8eab2debe79d // indirect
	github.com/json-iterator/go v0.0.0-20180701071628-ab8a2e0c74be // indirect
	github.com/kelseyhightower/envconfig v1.3.0
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.1 // indirect
	github.com/onsi/ginkgo v1.8.0
	github.com/onsi/gomega v1.5.0
	github.com/pborman/uuid v1.2.0 // indirect
	github.com/peterbourgon/diskv v0.0.0-20170814173558-5f041e8faa00 // indirect
	github.com/pkg/errors v0.8.1
	github.com/rdegges/go-ipify v0.0.0-20150526035502-2d94a6a86c40
	github.com/spf13/pflag v0.0.0-20180412120913-583c0c0531f0 // indirect
	github.com/vishvananda/netlink v1.0.0
	github.com/vishvananda/netns v0.0.0-20180720170159-13995c7128cc // indirect
	golang.org/x/oauth2 v0.0.0-20170412232759-a6bd8cefa181 // indirect
	golang.org/x/time v0.0.0-20161028155119-f51c12702a4d // indirect
	google.golang.org/appengine v1.6.1 // indirect
	gopkg.in/inf.v0 v0.0.0-20150911125757-3887ee99ecf0 // indirect
	gopkg.in/yaml.v2 v2.2.2 // indirect
	k8s.io/api v0.0.0-20190222213804-5cb15d344471
	k8s.io/apimachinery v0.0.0-20190629003722-e20a3a656cff
	k8s.io/client-go v0.0.0-20190521190702-177766529176
	k8s.io/klog v0.0.0-20181108234604-8139d8cb77af
	k8s.io/kube-openapi v0.0.0-20181109181836-c59034cc13d5 // indirect
	sigs.k8s.io/yaml v0.0.0-20181102190223-fd68e9863619 // indirect
)

replace github.com/bronze1man/goStrongswanVici => github.com/mangelajo/goStrongswanVici v0.0.0-20190223031456-9a5ae4453bd
