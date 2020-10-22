package events

import (
	"k8s.io/client-go/informers"
	"k8s.io/klog"

	"github.com/kelseyhightower/envconfig"

	k8sInformersV1 "k8s.io/client-go/informers/core/v1"

	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	submarinerInformersV1 "github.com/submariner-io/submariner/pkg/client/informers/externalversions/submariner.io/v1"
)

type specification struct {
	Namespace   string
}


type Controller struct {
	env specification

	endpointInformer    submarinerInformersV1.EndpointInformer
	nodeInformer        k8sInformersV1.NodeInformer
}

func (c *Controller) Start(stopCh <-chan struct{}, masterURL, kubeconfig string) {

	err := envconfig.Process("submariner", &c.env)
	if err != nil {
		klog.Fatal(err)
	}

	var k8sInformerFactory informers.SharedInformerFactory
	var submarinerInformerFactory submarinerInformers.SharedInformerFactory

	cfg := buildRestConfig(masterURL, kubeconfig)

	c.endpointInformer, submarinerInformerFactory = createEndpointFactoryAndInformer(cfg, c.env.Namespace)
	c.nodeInformer, k8sInformerFactory = createNodeFactoryAndInformer(cfg)

	klog.Info("Starting Controller")

	submarinerInformerFactory.Start(stopCh)
	k8sInformerFactory.Start(stopCh)
}
