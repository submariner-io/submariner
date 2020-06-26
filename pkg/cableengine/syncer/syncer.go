package syncer

import (
	"fmt"
	"reflect"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	v1typed "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
)

type GatewaySyncer struct {
	client  v1typed.GatewayInterface
	engine  cableengine.Engine
	version string
}

var GatewayUpdateInterval = 5 * time.Second
var GatewayStaleTimeout = time.Duration(GatewayUpdateInterval.Seconds()) * 3 * time.Second

const updateTimestampAnnotation = "update-timestamp"

// NewEngine creates a new Engine for the local cluster
func NewGatewaySyncer(engine cableengine.Engine, client v1typed.GatewayInterface,
	version string) *GatewaySyncer {

	return &GatewaySyncer{
		client:  client,
		engine:  engine,
		version: version,
	}
}

func (s *GatewaySyncer) Run(stopCh <-chan struct{}) {
	go func() {
		wait.Until(s.syncGatewayStatus, GatewayUpdateInterval, stopCh)
		s.CleanupGatewayEntry()
	}()

	klog.Info("CableEngine syncer started")
}

func (i *GatewaySyncer) syncGatewayStatus() {

	klog.V(log.TRACE).Info("Running syncGatewayStatus()")

	gatewayObj, err := i.generateGatewayObject()
	if err != nil {
		klog.Errorf("error generating gateway object from driver connections: %s", err)
		return
	}

	existingGw, err := i.getLastSyncedGateway(gatewayObj.Name)

	// log and stop for any error different to a not-found error
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("error trying to read existing gateway from k8s api: %s", err)
		return
	}

	if err != nil && errors.IsNotFound(err) {
		klog.V(log.TRACE).Infof("Gateway object needs creation: %+v", gatewayObj)
		_, err = i.client.Create(gatewayObj)
		if err != nil {
			klog.Errorf("error creating Gateway object: %s", err)
			return
		}
	} else {
		if !reflect.DeepEqual(gatewayObj.Status, existingGw.Status) {
			klog.V(log.TRACE).Infof("Gateway object needs an update: %+v", gatewayObj)
			existingGw.Status = gatewayObj.Status
			existingGw.Annotations = gatewayObj.Annotations

			gw, err := i.client.Update(existingGw)
			if err != nil {
				klog.Errorf("error updating Gateway object: %s", err)
				return
			} else {
				klog.V(log.TRACE).Infof("Gateway updated correctly: %+v", gw)
			}
		} else {
			klog.V(log.TRACE).Info("Gateway object didn't need an update")
		}
	}

	if gatewayObj.Status.HAStatus == v1.HAStatusActive {
		err := i.cleanupStaleGatewayEntries(gatewayObj.Name)
		if err != nil {
			klog.Errorf("error cleaning up stale gateway entries: %s", err)
		}
	}
}

func (i *GatewaySyncer) cleanupStaleGatewayEntries(localGatewayName string) error {
	gateways, err := i.client.List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, gw := range gateways.Items {
		if gw.Name == localGatewayName {
			continue
		}

		stale, err := isGatewayStale(gw)
		if err != nil {
			// In this case we don't want to stop the cleanup loop and just log it
			klog.Errorf("error processing stale gateway: %+v , %s", gw, err)
		}
		if stale {
			err := i.client.Delete(gw.Name, &metav1.DeleteOptions{})
			if err != nil {
				// In this case we don't want to stop the cleanup loop and just log it
				klog.Errorf("error deleting stale gateway: %+v, %s", gw, err)
			}
			klog.Warningf("Deleted stale gateway: %s, didn't report for %s",
				gw.Name, GatewayStaleTimeout)
		}
	}
	return nil
}

func isGatewayStale(gateway v1.Gateway) (bool, error) {

	if gateway.ObjectMeta.Annotations == nil {
		return true, nil
	}

	timestamp, ok := gateway.ObjectMeta.Annotations[updateTimestampAnnotation]
	if !ok {
		return true, fmt.Errorf("%q annotation not found", updateTimestampAnnotation)
	}

	timestampInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return true, fmt.Errorf("error parsing update-timestamp: %s", err)
	}
	now := time.Now().UTC().Unix()

	return now >= (timestampInt + int64(GatewayStaleTimeout.Seconds())), nil
}

func (i *GatewaySyncer) getLastSyncedGateway(name string) (*v1.Gateway, error) {

	existingGw, err := i.client.Get(name, metav1.GetOptions{})
	klog.V(log.TRACE).Infof("getLastSyncedGateway: %+v", existingGw)
	return existingGw, err
}

func (i *GatewaySyncer) generateGatewayObject() (*v1.Gateway, error) {
	localEndpoint := i.engine.GetLocalEndpoint()

	gateway := v1.Gateway{
		Status: v1.GatewayStatus{
			Version:       i.version,
			LocalEndpoint: localEndpoint.Spec,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        localEndpoint.Spec.Hostname,
			Annotations: map[string]string{updateTimestampAnnotation: strconv.FormatInt(time.Now().UTC().Unix(), 10)}},
	}

	gateway.Status.HAStatus = i.engine.GetHAStatus()

	connections, err := i.engine.ListCableConnections()
	if err != nil {
		gateway.Status.StatusFailure = fmt.Sprintf("Error getting driver connections: %s", err)
		klog.Errorf("error getting driver connections: %s", err)
		return nil, err
	}
	gateway.Status.Connections = *connections

	klog.V(log.TRACE).Infof("generateGatewayObject: %+v", gateway)
	return &gateway, nil
}

// CleanupGatewayEntry removes this Gateway entry from the k8s API, it does not
// propagate error up because it's a termination function that we also provide externally
func (s *GatewaySyncer) CleanupGatewayEntry() {
	hostName := s.engine.GetLocalEndpoint().Spec.Hostname
	err := s.client.Delete(hostName, &metav1.DeleteOptions{})
	if err != nil {
		klog.Errorf("Error happened when trying to delete own Gateway entry %q : %s", hostName, err)
		return
	}
	klog.Infof("The Gateway entry for %q has been removed", hostName)
}
