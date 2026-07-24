/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cilium

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/pkg/errors"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
)

const (
	defaultEtcdReadyTimeout = 60 * time.Second
	defaultEtcdDialTimeout  = 5 * time.Second
)

// EtcdStoreConfig configures an embedded etcd used as a ClusterMesh peer.
type EtcdStoreConfig struct {
	DataDir            string
	ListenClientURL    string
	AdvertiseClientURL string
	ListenPeerURL      string
	AdvertisePeerURL   string
	Name               string
	CertFile           string
	KeyFile            string
	CAFile             string
	ClientCertAuth     bool
}

type etcdStore struct {
	etcd          *embed.Etcd
	client        *clientv3.Client
	dataDir       string
	removeDataDir bool
	closed        bool
}

func startEtcdStore(ctx context.Context, cfg *EtcdStoreConfig) (*etcdStore, error) {
	setEtcdStoreDefaults(cfg)

	removeDataDir, err := prepareEtcdDataDir(cfg)
	if err != nil {
		return nil, err
	}

	ecfg, lc, err := newEmbedEtcdConfig(cfg)
	if err != nil {
		if removeDataDir {
			_ = os.RemoveAll(cfg.DataDir)
		}

		return nil, err
	}

	e, err := embed.StartEtcd(ecfg)
	if err != nil {
		if removeDataDir {
			_ = os.RemoveAll(cfg.DataDir)
		}

		return nil, errors.Wrap(err, "start embedded etcd")
	}

	cleanupFailedStart := func() {
		e.Close()

		if removeDataDir {
			_ = os.RemoveAll(cfg.DataDir)
		}
	}

	select {
	case <-e.Server.ReadyNotify():
	case <-time.After(defaultEtcdReadyTimeout):
		cleanupFailedStart()
		return nil, errors.New("embedded etcd ready timeout")
	case <-ctx.Done():
		cleanupFailedStart()
		return nil, errors.Wrap(ctx.Err(), "waiting for embedded etcd")
	}

	cCfg, err := newEtcdClientConfig(cfg, ecfg, lc)
	if err != nil {
		cleanupFailedStart()
		return nil, err
	}

	cli, err := clientv3.New(*cCfg)
	if err != nil {
		cleanupFailedStart()
		return nil, errors.Wrap(err, "create etcd client")
	}

	return &etcdStore{
		etcd:          e,
		client:        cli,
		dataDir:       cfg.DataDir,
		removeDataDir: removeDataDir,
	}, nil
}

func newEtcdClientConfig(cfg *EtcdStoreConfig, ecfg *embed.Config, lc *url.URL) (*clientv3.Config, error) {
	cCfg := &clientv3.Config{
		Endpoints:   []string{cfg.AdvertiseClientURL},
		DialTimeout: defaultEtcdDialTimeout,
	}

	// Prefer loopback for the in-process client so TLS ServerName matches the
	// publisher cert SANs (127.0.0.1 / localhost).
	switch lc.Hostname() {
	case "0.0.0.0", "", "127.0.0.1", "localhost":
		cCfg.Endpoints = []string{fmt.Sprintf("%s://127.0.0.1:%s", lc.Scheme, lc.Port())}
	}

	if ecfg.ClientTLSInfo.Empty() {
		return cCfg, nil
	}

	tlsCfg, err := ecfg.ClientTLSInfo.ClientConfig()
	if err != nil {
		return nil, errors.Wrap(err, "etcd client TLS")
	}

	// Keep certificate verification; pin ServerName to the loopback SAN.
	tlsCfg.ServerName = "localhost"
	cCfg.TLS = tlsCfg

	return cCfg, nil
}

func setEtcdStoreDefaults(cfg *EtcdStoreConfig) {
	if cfg.Name == "" {
		cfg.Name = "submariner-cilium-cm"
	}

	if cfg.ListenClientURL == "" {
		cfg.ListenClientURL = "http://127.0.0.1:12379"
	}

	if cfg.AdvertiseClientURL == "" {
		cfg.AdvertiseClientURL = cfg.ListenClientURL
	}

	if cfg.ListenPeerURL == "" {
		cfg.ListenPeerURL = "http://127.0.0.1:12380"
	}

	if cfg.AdvertisePeerURL == "" {
		cfg.AdvertisePeerURL = cfg.ListenPeerURL
	}
}

func prepareEtcdDataDir(cfg *EtcdStoreConfig) (bool, error) {
	if cfg.DataDir != "" {
		return false, errors.Wrap(os.MkdirAll(cfg.DataDir, 0o700), "mkdir etcd data dir")
	}

	dir, err := os.MkdirTemp("", "submariner-cilium-cm-etcd-")
	if err != nil {
		return false, errors.Wrap(err, "create etcd data dir")
	}

	cfg.DataDir = dir

	return true, nil
}

func newEmbedEtcdConfig(cfg *EtcdStoreConfig) (*embed.Config, *url.URL, error) {
	lc, err := url.Parse(cfg.ListenClientURL)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse listen client URL")
	}

	ac, err := url.Parse(cfg.AdvertiseClientURL)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse advertise client URL")
	}

	lp, err := url.Parse(cfg.ListenPeerURL)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse listen peer URL")
	}

	ap, err := url.Parse(cfg.AdvertisePeerURL)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse advertise peer URL")
	}

	ecfg := embed.NewConfig()
	ecfg.Name = cfg.Name
	ecfg.Dir = cfg.DataDir
	ecfg.Logger = "zap"
	ecfg.LogLevel = "warn"
	ecfg.ListenClientUrls = []url.URL{*lc}
	ecfg.AdvertiseClientUrls = []url.URL{*ac}
	ecfg.ListenPeerUrls = []url.URL{*lp}
	ecfg.AdvertisePeerUrls = []url.URL{*ap}
	ecfg.InitialCluster = ecfg.InitialClusterFromName(ecfg.Name)

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		ecfg.ClientTLSInfo = transport.TLSInfo{
			CertFile:       cfg.CertFile,
			KeyFile:        cfg.KeyFile,
			TrustedCAFile:  cfg.CAFile,
			ClientCertAuth: cfg.ClientCertAuth,
		}
	}

	return ecfg, lc, nil
}

func (s *etcdStore) Bootstrap(ctx context.Context, remoteName string, clusterID uint32) error {
	b, err := marshalClusterConfig(defaultClusterConfig(clusterID))
	if err != nil {
		return errors.Wrap(err, "marshal cluster-config")
	}

	key := clusterConfigKey(remoteName)
	if _, err := s.client.Put(ctx, key, string(b)); err != nil {
		return errors.Wrapf(err, "put cluster-config %q", key)
	}

	return nil
}

func (s *etcdStore) UpsertRoute(ctx context.Context, cidrStr, hostIP string, clusterID uint32) error {
	pair, key, err := buildIPIdentityPair(cidrStr, hostIP, clusterID)
	if err != nil {
		return err
	}

	b, err := marshalIPIdentityPair(pair)
	if err != nil {
		return errors.Wrap(err, "marshal IPIdentityPair")
	}

	if _, err := s.client.Put(ctx, key, string(b)); err != nil {
		return errors.Wrapf(err, "put route %q", key)
	}

	return nil
}

func (s *etcdStore) DeleteRoute(ctx context.Context, cidrStr string) error {
	ip, mask, err := parseCIDR(cidrStr)
	if err != nil {
		return errors.Wrap(err, "parse CIDR")
	}

	key := ipIdentityKey(prefixString(&ipIdentityPair{IP: ip, Mask: mask}))
	if _, err := s.client.Delete(ctx, key); err != nil {
		return errors.Wrapf(err, "delete route %q", key)
	}

	return nil
}

func (s *etcdStore) DeleteClusterConfig(ctx context.Context, remoteName string) error {
	key := clusterConfigKey(remoteName)
	if _, err := s.client.Delete(ctx, key); err != nil {
		return errors.Wrapf(err, "delete cluster-config %q", key)
	}

	return nil
}

func (s *etcdStore) TouchHeartbeat(ctx context.Context) error {
	value := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.client.Put(ctx, cmHeartbeatKey, value); err != nil {
		return errors.Wrapf(err, "put heartbeat %q", cmHeartbeatKey)
	}

	return nil
}

func (s *etcdStore) Close() error {
	if s == nil || s.closed {
		return nil
	}

	s.closed = true

	var errs []error

	if s.client != nil {
		if err := s.client.Close(); err != nil {
			errs = append(errs, err)
		}

		s.client = nil
	}

	if s.etcd != nil {
		s.etcd.Close()
		s.etcd = nil
	}

	if s.removeDataDir && s.dataDir != "" {
		if err := os.RemoveAll(s.dataDir); err != nil {
			errs = append(errs, errors.Wrap(err, "remove etcd data dir"))
		}
	}

	if len(errs) > 0 {
		return errors.Errorf("close etcd store: %v", errs)
	}

	return nil
}
