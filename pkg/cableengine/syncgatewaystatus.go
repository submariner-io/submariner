package cableengine

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/log"
)

const GatewayUpdateIntervalSeconds = 5
const GatewayStaleTimeoutSeconds = GatewayUpdateIntervalSeconds * 3
const updateTimestampAnnotation = "update-timestamp"

func (i *engine) syncGatewayStatus() {

	i.Lock()
	defer i.Unlock()

	klog.V(log.TRACE).Info("Running syncGatewayStatus()")

	gatewayObj, err := i.generateGatewayObject()
	if err != nil {
		klog.Errorf("generating gateway object from driver connections: %s", err)
		return
	}

	existingGw, err := i.getLastSyncedGateway(gatewayObj.Name)

	// log and stop for any error different to a not-found error
	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("trying to read existing gateway from k8s api: %s", err)
		return
	}

	gwClient := i.clientset.SubmarinerV1().Gateways(i.namespace)

	if err != nil && errors.IsNotFound(err) {
		klog.V(log.TRACE).Infof("Gateway object needs creation: %+v", gatewayObj)
		_, err = gwClient.Create(gatewayObj)
		if err != nil {
			klog.Errorf("creating Gateway object: %s", err)
			return
		}
	} else {
		if !reflect.DeepEqual(gatewayObj.Status, existingGw.Status) {
			klog.V(log.TRACE).Infof("Gateway object needs an update: %+v", gatewayObj)
			existingGw.Status = gatewayObj.Status
			existingGw.Labels = gatewayObj.Labels
			existingGw.Annotations = gatewayObj.Annotations

			gw, err := gwClient.Update(existingGw)
			if err != nil {
				klog.Errorf("updating Gateway object: %s", err)
				return
			} else {
				klog.V(log.TRACE).Infof("Gateway updated correctly: %+v", gw)
			}
		} else {
			klog.V(log.TRACE).Info("Gateway object didn't need an update")
		}
	}

	if gatewayObj.Status.HAStatus == v1.HAStatusActive {
		err := i.cleanupStaleGatewayEntries()
		if err != nil {
			klog.Errorf("cleaning up stale gateway entries: %s", err)
		}
	}
}

func (i *engine) cleanupStaleGatewayEntries() error {
	gwClient := i.clientset.SubmarinerV1().Gateways(i.namespace)
	gateways, err := gwClient.List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, gw := range gateways.Items {
		stale, err := isGatewayStale(gw)
		if err != nil {
			// In this case we don't want to stop the cleanup loop and just log it
			klog.Errorf("error processing stale gateway: %+v , %s", gw, err)
		}
		if stale {
			err := gwClient.Delete(gw.Name, &metav1.DeleteOptions{})
			if err != nil {
				// In this case we don't want to stop the cleanup loop and just log it
				klog.Errorf("error deleting stale gateway: %+v, %s", gw, err)
			}
			klog.Warningf("deleted stale gateway: %s, didn't report for %d seconds",
				gw.Name, GatewayStaleTimeoutSeconds)
		}
	}
	return nil
}

func isGatewayStale(gateway v1.Gateway) (bool, error) {

	timestamp, ok := gateway.ObjectMeta.Annotations[updateTimestampAnnotation]
	if !ok {
		return true, fmt.Errorf("update-timestamp annotation not found")
	}

	timestampInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return true, fmt.Errorf("error parsing update-timestamp: %s", err)
	}
	now := time.Now().UTC().Unix()

	return now >= (timestampInt + GatewayStaleTimeoutSeconds), nil
}

func (i *engine) getLastSyncedGateway(name string) (*v1.Gateway, error) {

	existingGw, err := i.clientset.SubmarinerV1().Gateways(i.namespace).Get(name, metav1.GetOptions{})
	klog.V(log.TRACE).Infof("getLastSyncedGateway: %+v", existingGw)
	return existingGw, err
}

func (i *engine) generateGatewayObject() (*v1.Gateway, error) {
	var hostName string
	var err error

	if hostName, err = os.Hostname(); err != nil {
		return nil, err
	}

	gateway := v1.Gateway{
		Status: v1.GatewayStatus{
			Version:       i.version,
			LocalEndpoint: i.localEndpoint.Spec,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        hostName,
			Annotations: map[string]string{updateTimestampAnnotation: strconv.FormatInt(time.Now().UTC().Unix(), 10)}},
	}

	if i.driver == nil {
		gateway.Status.HAStatus = v1.HAStatusPassive
		gateway.SetLabels(map[string]string{"ha-status": "passive"})
	} else {
		gateway.Status.HAStatus = v1.HAStatusActive
		gateway.SetLabels(map[string]string{"ha-status": "active"})
		connections, err := i.driver.GetConnections()
		if err != nil {
			klog.Errorf("getting driver connections: %s", err)
			return nil, err
		}
		gateway.Status.Connections = *connections
	}

	klog.V(log.TRACE).Infof("generateGatewayObject: %+v", gateway)
	return &gateway, nil
}
