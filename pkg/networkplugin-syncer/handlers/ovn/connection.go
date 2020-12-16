package ovn

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"

	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"

	"github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn/nbctl"
	"github.com/submariner-io/submariner/pkg/util/cluster_files"
)

func (ovn *SyncHandler) initClients() error {
	certFile, err := cluster_files.Get(ovn.k8sClientset, getOVNCertPath())
	if err != nil {
		return err
	}

	pkFile, err := cluster_files.Get(ovn.k8sClientset, getOVNPrivKeyPath())
	if err != nil {
		return err
	}

	caFile, err := cluster_files.Get(ovn.k8sClientset, getOVNCaBundlePath())
	if err != nil {
		return err
	}

	ovn.nbctl = nbctl.New(getOVNNBDBAddress(), pkFile, certFile, caFile)

	tlsConfig, err := getOVNTLSConfig(pkFile, certFile, caFile)
	if err != nil {
		return err
	}

	ovn.nbdb, err = goovn.NewClient(&goovn.Config{
		Addr:      getOVNNBDBAddress(),
		Reconnect: true,
		TLSConfig: tlsConfig,
		Db:        goovn.DBNB})

	if err != nil {
		return errors.Wrap(err, "error creating NBDB connection")
	}

	ovn.sbdb, err = goovn.NewClient(&goovn.Config{
		Addr:      getOVNSBDBAddress(),
		Reconnect: true,
		TLSConfig: tlsConfig,
		Db:        goovn.DBSB})

	if err != nil {
		return errors.Wrap(err, "error creating SBDB connection")
	}

	return nil
}

func getOVNTLSConfig(pkFile, certFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, pkFile)
	if err != nil {
		return nil, errors.Wrap(err, "Failure loading ovn certificates")
	}

	rootCAs := x509.NewCertPool()

	data, err := ioutil.ReadFile(caFile)

	if err != nil {
		return nil, errors.Wrap(err, "failure loading OVNDB ca bundle")
	}

	rootCAs.AppendCertsFromPEM(data)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      rootCAs,
		ServerName:   "ovn",
	}, nil
}
