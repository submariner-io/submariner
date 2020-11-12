package ovn

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"os"

	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"

	"github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn/nbctl"
)

const (
	ovnCert        = "/ovn-cert/tls.crt"
	ovnPrivKey     = "/ovn-cert/tls.key"
	ovnCABundle    = "/ovn-ca/ca-bundle.crt"
	defaultOVNNBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9641"
	defaultOVNSBDB = "ssl:ovnkube-db.openshift-ovn-kubernetes.svc.cluster.local:9642"
)

func (ovn *SyncHandler) initClients() error {
	tlsConfig, err := getOVNTLSConfig()
	if err != nil {
		return err
	}

	ovn.nbctl = nbctl.New(defaultOVNNBDB, ovnPrivKey, ovnCert, ovnCABundle)

	ovn.nbdb, err = goovn.NewClient(&goovn.Config{
		Addr:      getOVNNBDBAddress(),
		Reconnect: true,
		TLSConfig: tlsConfig,
		Db:        goovn.DBNB})

	if err != nil {
		return errors.Wrap(err, "Error creating NBDB connection")
	}

	ovn.sbdb, err = goovn.NewClient(&goovn.Config{
		Addr:      getOVNSBDBAddress(),
		Reconnect: true,
		TLSConfig: tlsConfig,
		Db:        goovn.DBSB})

	if err != nil {
		return errors.Wrap(err, "Error creating SBDB connection")
	}

	return nil
}

func getOVNNBDBAddress() string {
	addr := os.Getenv("OVN_NBDB")
	if addr == "" {
		return defaultOVNNBDB
	}

	return addr
}

func getOVNSBDBAddress() string {
	addr := os.Getenv("OVN_SBDB")
	if addr == "" {
		return defaultOVNSBDB
	}

	return addr
}

func getOVNTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(ovnCert, ovnPrivKey)
	if err != nil {
		return nil, errors.Wrap(err, "Failure loading ovn certificates")
	}

	rootCAs := x509.NewCertPool()

	data, err := ioutil.ReadFile(ovnCABundle)

	if err != nil {
		return nil, errors.Wrap(err, "Failure loading OVNDB CA bundle")
	}

	rootCAs.AppendCertsFromPEM(data)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      rootCAs,
		ServerName:   "ovn",
	}, nil
}
