package statusserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"k8s.io/klog"

	v1 "github.com/submariner-io/submariner/pkg/apis/status/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
)

type StatusServer struct {
	version     string
	httpServer  *http.Server
	cableEngine cableengine.Engine
}

func New(address string, version string) *StatusServer {
	statusServer := &StatusServer{
		httpServer: &http.Server{Addr: address},
		version:    version,
	}
	statusServer.httpServer.Handler = statusServer

	return statusServer
}

func (hs *StatusServer) SetCableEngine(engine cableengine.Engine) {
	hs.cableEngine = engine
}

func (hs *StatusServer) Run(stop <-chan struct{}) {

	listenAndServeFailed := make(chan struct{})

	go func() {
		err := hs.httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			close(listenAndServeFailed)
			klog.Fatalf("Unable to start status server: %s", err.Error())
		}
		klog.Infof("Started status server on %s", hs.httpServer.Addr)
	}()

	<-stop
	select {
	case <-listenAndServeFailed:
	// the server didn't start, no need to shutdown
	default:
		hs.shutdown()
	}
}

func (hs *StatusServer) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := hs.httpServer.Shutdown(ctx)
	if err != nil {
		klog.Errorf("Error shutting down status server: %s", err.Error())
	}
}

func (hs *StatusServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	if path == "/health" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	} else if path == "/status" {
		hs.handleStatusResponse(w, r)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

func (hs *StatusServer) handleStatusResponse(w http.ResponseWriter, r *http.Request) {
	status, err := hs.getStatus()
	if err != nil {
		hs.errorResponse(w, err)
	}

	js, err := json.Marshal(status)
	if err != nil {
		hs.errorResponse(w, err)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(js)
}

func (hs *StatusServer) errorResponse(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(err.Error()))
}

func (hs *StatusServer) getStatus() (v1.GatewayStatus, error) {

	status := v1.GatewayStatus{Version: hs.version}

	hostName, err := os.Hostname()
	if err != nil {
		return status, err
	}

	status.Host = hostName

	// In our current Active/Passive implementation we only have a cable engine to query
	// if we are an active submariner gateway
	if hs.cableEngine == nil {
		status.HAStatus = v1.HAStatusPassive
		return status, nil
	}

	status.HAStatus = v1.HAStatusActive

	connections, err := hs.cableEngine.ListCableConnections()
	status.Connections = *connections

	return status, err
}
