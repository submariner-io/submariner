package healthchecker

import (
	"sync"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

type LatencyInfo struct {
	ConnectionError string
	Spec            *submarinerv1.LatencySpec
}

type Interface interface {
	Start(stopCh <-chan struct{})

	GetLatencyInfo(hostName string) *LatencyInfo
}

type HealthChecker struct {
	endpointWatcher watcher.Interface
	healthCheckers  sync.Map
	clusterID       string
}

func New(config *watcher.Config, endpointNameSpace, clusterID string) (*HealthChecker, error) {
	serviceExportController := HealthChecker{
		clusterID: clusterID,
	}

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "HelathChecker Endoint Controller",
			ResourceType: &submarinerv1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: serviceExportController.endpointCreatedorUpdated,
				OnUpdateFunc: serviceExportController.endpointCreatedorUpdated,
				OnDeleteFunc: serviceExportController.endpointDeleted,
			},
			SourceNamespace: endpointNameSpace,
		},
	}

	endpointWatcher, err := watcher.New(config)
	if err != nil {
		return nil, err
	}

	serviceExportController.endpointWatcher = endpointWatcher

	return &serviceExportController, nil
}

func (h *HealthChecker) GetLatencyInfo(hostName string) *LatencyInfo {
	if obj, found := h.healthCheckers.Load(hostName); found {
		pinger := obj.(*Pinger)
		statistics := pinger.statistics

		return &LatencyInfo{
			ConnectionError: pinger.Status,
			Spec: &submarinerv1.LatencySpec{
				LastRTT:    statistics.lastRtt,
				MinRTT:     statistics.minRtt,
				AverageRTT: statistics.mean,
				MaxRTT:     statistics.maxRtt,
				StdDevRTT:  statistics.stdDev,
			},
		}
	}

	return nil
}

func (h *HealthChecker) Start(stopCh <-chan struct{}) error {
	if err := h.endpointWatcher.Start(stopCh); err != nil {
		return err
	}

	return nil
}

func (h *HealthChecker) endpointCreatedorUpdated(obj runtime.Object) bool {
	klog.V(log.TRACE).Infof("Endpoint created: %v", obj)
	endpointCreated := obj.(*submarinerv1.Endpoint)
	if endpointCreated.Spec.ClusterID == h.clusterID {
		klog.Infof("Skipping endpoint creation for the local cluster %q", h.clusterID)
		return false
	}

	if endpointCreated.Spec.HealthCheckIP == "" || endpointCreated.Spec.Hostname == "" {
		klog.Infof("HealthCheckIP or Hostname is Nil, hence returning %q: %q", endpointCreated.Spec.HealthCheckIP,
			endpointCreated.Spec.Hostname)
		return false
	}

	if obj, found := h.healthCheckers.Load(endpointCreated.Spec.Hostname); found {
		klog.V(log.TRACE).Infof("HealthChecker is already running %q: %q, and hence stopping", endpointCreated.Spec.HealthCheckIP,
			endpointCreated.Spec.Hostname)

		pinger := obj.(*Pinger)
		if pinger.healthCheckIp == endpointCreated.Spec.HealthCheckIP {
			return false
		}

		close(pinger.stopCh)
		pinger.stop()
		h.healthCheckers.Delete(endpointCreated.Spec.Hostname)
	}

	klog.V(log.TRACE).Infof("Starting Pinger for Hostname: %q, with HealthCheckIP: %q",
		endpointCreated.Spec.HealthCheckIP, endpointCreated.Spec.Hostname)

	pinger := NewPinger(endpointCreated.Spec.Hostname, endpointCreated.Spec.HealthCheckIP)
	h.healthCheckers.Store(endpointCreated.Spec.Hostname, pinger)
	pinger.Run()

	return false
}

func (h *HealthChecker) endpointDeleted(obj runtime.Object) bool {
	endpointDeleted := obj.(*submarinerv1.Endpoint)
	if endpointDeleted.Spec.Hostname == "" {
		return false
	}

	if obj, found := h.healthCheckers.Load(endpointDeleted.Spec.Hostname); found {
		pinger := obj.(*Pinger)
		pinger.stop()
		h.healthCheckers.Delete(endpointDeleted.Spec.Hostname)
	}

	return false
}
