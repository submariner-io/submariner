package healthchecker

import (
	"strconv"
	"sync"
	"time"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

type LatencyInfo struct {
	ConnectionError string
	Spec            *submarinerv1.LatencyRTTSpec
}

type Interface interface {
	Start(stopCh <-chan struct{}) error

	GetLatencyInfo(endpoint *submarinerv1.EndpointSpec) *LatencyInfo
}

type controller struct {
	endpointWatcher    watcher.Interface
	pingers            sync.Map
	clusterID          string
	pingInterval       uint
	maxPacketLossCount uint
}

func New(config *watcher.Config, endpointNameSpace, clusterID string, pingInterval, maxPacketLossCount uint) (Interface, error) {
	controller := &controller{
		clusterID:          clusterID,
		pingInterval:       pingInterval,
		maxPacketLossCount: maxPacketLossCount,
	}
	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "HealthChecker Endpoint Controller",
			ResourceType: &submarinerv1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: controller.endpointCreatedorUpdated,
				OnUpdateFunc: controller.endpointCreatedorUpdated,
				OnDeleteFunc: controller.endpointDeleted,
			},
			SourceNamespace: endpointNameSpace,
		},
	}

	endpointWatcher, err := watcher.New(config)

	if err != nil {
		return nil, err
	}

	controller.endpointWatcher = endpointWatcher

	return controller, nil
}

func (h *controller) GetLatencyInfo(endpoint *submarinerv1.EndpointSpec) *LatencyInfo {
	if obj, found := h.pingers.Load(endpoint.CableName); found {
		pinger := obj.(*pingerInfo)

		lastTime, _ := time.ParseDuration(strconv.FormatUint(pinger.statistics.lastRtt, 10) + "ns")
		minTime, _ := time.ParseDuration(strconv.FormatUint(pinger.statistics.minRtt, 10) + "ns")
		averageTime, _ := time.ParseDuration(strconv.FormatUint(pinger.statistics.mean, 10) + "ns")
		maxTime, _ := time.ParseDuration(strconv.FormatUint(pinger.statistics.maxRtt, 10) + "ns")
		stdDevTime, _ := time.ParseDuration(strconv.FormatUint(pinger.statistics.stdDev, 10) + "ns")

		return &LatencyInfo{
			ConnectionError: pinger.failureMsg,
			Spec: &submarinerv1.LatencyRTTSpec{
				Last:    lastTime.String(),
				Min:     minTime.String(),
				Average: averageTime.String(),
				Max:     maxTime.String(),
				StdDev:  stdDevTime.String(),
			},
		}
	}

	return nil
}

func (h *controller) Start(stopCh <-chan struct{}) error {
	if err := h.endpointWatcher.Start(stopCh); err != nil {
		return err
	}

	return nil
}

func (h *controller) endpointCreatedorUpdated(obj runtime.Object) bool {
	klog.V(log.TRACE).Infof("Endpoint created: %#v", obj)
	endpointCreated := obj.(*submarinerv1.Endpoint)
	if endpointCreated.Spec.ClusterID == h.clusterID {
		return false
	}

	if endpointCreated.Spec.HealthCheckIP == "" || endpointCreated.Spec.CableName == "" {
		klog.Infof("HealthCheckIP (%q) and/or CableName (%q) for Endpoint %q empty - will not monitor endpoint health",
			endpointCreated.Spec.HealthCheckIP, endpointCreated.Spec.CableName, endpointCreated.Name)
		return false
	}

	if obj, found := h.pingers.Load(endpointCreated.Spec.CableName); found {
		pinger := obj.(*pingerInfo)
		if pinger.healthCheckIP == endpointCreated.Spec.HealthCheckIP {
			return false
		}

		klog.V(log.DEBUG).Infof("HealthChecker is already running for %q - stopping", endpointCreated.Name)
		pinger.stop()
		h.pingers.Delete(endpointCreated.Spec.CableName)
	}

	klog.V(log.TRACE).Infof("Starting Pinger for CableName: %q, with HealthCheckIP: %q",
		endpointCreated.Spec.CableName, endpointCreated.Spec.HealthCheckIP)

	pingInterval := DefaultPingInterval
	if h.pingInterval != 0 {
		pingInterval = time.Second * time.Duration(h.pingInterval)
	}

	maxPacketLossCount := DefaultMaxPacketLossCount

	if h.maxPacketLossCount != 0 {
		maxPacketLossCount = h.maxPacketLossCount
	}

	pinger := newPinger(endpointCreated.Spec.HealthCheckIP, pingInterval, maxPacketLossCount)
	h.pingers.Store(endpointCreated.Spec.CableName, pinger)
	pinger.start()

	return false
}

func (h *controller) endpointDeleted(obj runtime.Object) bool {
	endpointDeleted := obj.(*submarinerv1.Endpoint)
	if endpointDeleted.Spec.CableName == "" {
		return false
	}

	if obj, found := h.pingers.Load(endpointDeleted.Spec.CableName); found {
		pinger := obj.(*pingerInfo)
		pinger.stop()
		h.pingers.Delete(endpointDeleted.Spec.CableName)
	}

	return false
}
