package mtu

import (
	"bytes"
	"io/ioutil"

	"k8s.io/klog"

	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/event"
)

type mtuHandler struct {
	event.HandlerBase
}

func NewMTUHandler() event.Handler {
	return &mtuHandler{}
}

func (h *mtuHandler) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (h *mtuHandler) GetName() string {
	return "MTU handler"
}

func (h *mtuHandler) Init() error {
	klog.Infof("Updating configurations for Path MTU")
	configureTCPMTUProbe()

	return nil
}

func configureTCPMTUProbe() {
	mtuProbe := "2"
	baseMss := "1024"

	err := ConfigureTCPMTUProbe(mtuProbe, baseMss)
	if err != nil {
		klog.Warningf(err.Error())
	}
}

func ConfigureTCPMTUProbe(mtuProbe, baseMss string) error {
	err := setSysctl("/proc/sys/net/ipv4/tcp_mtu_probing", []byte(mtuProbe))
	if err != nil {
		return errors.WithMessagef(err, "unable to update value of tcp_mtu_probing to %s", mtuProbe)
	}

	err = setSysctl("/proc/sys/net/ipv4/tcp_base_mss", []byte(baseMss))

	return errors.WithMessagef(err, "unable to update value of tcp_base_mss to %ss", baseMss)
}

func setSysctl(path string, contents []byte) error {
	existing, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	// Ignore leading and terminating newlines
	existing = bytes.Trim(existing, "\n")

	if bytes.Equal(existing, contents) {
		return nil
	}
	// Permissions are already 644, the files are never created
	// #nosec G306
	return ioutil.WriteFile(path, contents, 0644)
}
