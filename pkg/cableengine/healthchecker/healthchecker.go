package healthchecker

import (
	"sync"
	"time"

	"github.com/go-ping/ping"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	v1typed "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

var HealthCheckInterval = 15 * time.Second

type HealthChecker struct {
	localEndpointName string
	client            v1typed.GatewayInterface
	latencyMap        *sync.Map
}

type LatencyInfo struct {
	Status  v1.ConnectionStatus
	Latency *v1.LatencySpec
}

// NewEngine creates a new Engine for the local cluster
func NewHealthChecker(localEndpointName string, client v1typed.GatewayInterface,
	latencyMap *sync.Map) *HealthChecker {
	return &HealthChecker{
		localEndpointName: localEndpointName,
		client:            client,
		latencyMap:        latencyMap,
	}
}

func (h *HealthChecker) Run(stopCh <-chan struct{}) {
	go func() {
		wait.Until(h.checkRemoteGatewayHealth, HealthCheckInterval, stopCh)
	}()

	klog.Infof("CableEngine healthchecker started")
}
func (h *HealthChecker) checkRemoteGatewayHealth() {
	existingGw, err := h.client.Get(h.localEndpointName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		klog.V(2).Infof("Gateway object not found: %v", err)
		return
	} else if err != nil {
		klog.Errorf("Error retrieving the gateway object: %v", err)
		return
	}

	if len(existingGw.Status.Connections) == 0 {
		klog.V(2).Info("No endpoints present in the gateway")
		return
	}

	deleteMap := copyLatencyMap(h.latencyMap)
	var wg sync.WaitGroup

	for _, endPointInfo := range existingGw.Status.Connections {
		wg.Add(1)

		hostName := endPointInfo.Endpoint.Hostname
		healthCheckIp := endPointInfo.Endpoint.HealthCheckIP

		// Remove the entry that is found in the gateway connection status
		delete(deleteMap, hostName)

		go func() {
			latencyInfo := h.sendPing(&wg, hostName, healthCheckIp)
			if latencyInfo != nil {
				h.latencyMap.Store(hostName, latencyInfo)
			}
		}()
	}

	wg.Wait()

	for k := range deleteMap {
		// Remove the entry that are missing in the gateway connection status from the
		// latencymap
		h.latencyMap.Delete(k)
	}
}

func copyLatencyMap(latencyMap *sync.Map) map[string]*LatencyInfo {
	newMap := make(map[string]*LatencyInfo)

	latencyMap.Range(func(key, val interface{}) bool {
		if val != nil {
			newMap[key.(string)] = val.(*LatencyInfo)
		}
		return true
	})

	return newMap
}

func (h *HealthChecker) sendPing(wg *sync.WaitGroup, remoteHostName, healthCheckIp string) *LatencyInfo {
	defer wg.Done()
	var lastRtt time.Duration

	pinger, err := ping.NewPinger(healthCheckIp)
	if err != nil {
		klog.Errorf("Creating pinger for hostname %v to the ip %v failed due to %v", remoteHostName, healthCheckIp, err)
		return nil
	}

	pinger.Count = 3
	pinger.SetPrivileged(true)
	pinger.Timeout = 3 * time.Second

	pinger.OnRecv = func(packet *ping.Packet) {
		lastRtt = packet.Rtt
	}
	var latencyinfo *LatencyInfo

	pinger.OnFinish = func(stats *ping.Statistics) {
		if stats.PacketLoss == 100 {
			latencySpec := &v1.LatencySpec{
				LastRTT:    0,
				MinRTT:     0,
				AverageRTT: 0,
				MaxRTT:     0,
				StdDevRTT:  0,
			}
			latencyinfo = &LatencyInfo{
				Status:  v1.ConnectionError,
				Latency: latencySpec,
			}
		} else {
			latencySpec := &v1.LatencySpec{
				LastRTT:    float64(lastRtt.Microseconds()) / 1000,
				MinRTT:     float64(stats.MinRtt.Microseconds()) / 1000,
				AverageRTT: float64(stats.AvgRtt.Microseconds()) / 1000,
				MaxRTT:     float64(stats.MaxRtt.Microseconds()) / 1000,
				StdDevRTT:  float64(stats.StdDevRtt.Microseconds()) / 1000,
			}
			latencyinfo = &LatencyInfo{
				Status:  v1.Connected,
				Latency: latencySpec,
			}
		}
	}
	err = pinger.Run()
	if err != nil {
		klog.Errorf("Ping to %q with ip %q failed due to  %v", remoteHostName, healthCheckIp, err)
	}

	return latencyinfo
}
