package cableengine

import (
	"os"
	"reflect"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/log"
)

const GatewayUpdateIntervalSeconds = 5

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
		Status:     v1.GatewayStatus{Version: i.version, LocalEndpoint: i.localEndpoint.Spec},
		ObjectMeta: metav1.ObjectMeta{Name: hostName},
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
