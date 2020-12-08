package ovn

import "os"

const (
	ovnCert        = "secret://openshift-ovn-kubernetes/ovn-cert/tls.crt"
	ovnPrivKey     = "secret://openshift-ovn-kubernetes/ovn-cert/tls.key"
	ovnCABundle    = "configmap://openshift-ovn-kubernetes/ovn-ca/ca-bundle.crt"
	defaultOVNNBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9641"
	defaultOVNSBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9642"
)

func getOVNSBDBAddress() string {
	addr := os.Getenv("OVN_SBDB")
	if addr == "" {
		return defaultOVNSBDB
	}

	return addr
}

func getOVNNBDBAddress() string {
	addr := os.Getenv("OVN_NBDB")
	if addr == "" {
		return defaultOVNNBDB
	}

	return addr
}

func getOVNPrivKeyPath() string {
	key := os.Getenv("OVN_PK")
	if key == "" {
		return ovnPrivKey
	}

	return key
}

func getOVNCertPath() string {
	cert := os.Getenv("OVN_CERT")
	if cert == "" {
		return ovnCert
	}

	return cert
}

func getOVNCaBundlePath() string {
	ca := os.Getenv("OVN_CA")
	if ca == "" {
		return ovnCABundle
	}

	return ca
}
