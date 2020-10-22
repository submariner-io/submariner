package events

import (
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	v12 "github.com/submariner-io/submariner/pkg/client/informers/externalversions/submariner.io/v1"
)

const defaultInformerResyncTime = 10 * time.Minute

func createNodeFactoryAndInformer(cfg *rest.Config) (
	v1.NodeInformer, informers.SharedInformerFactory) {
	k8sClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building clientset: %s", err.Error())
	}
	k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(k8sClientSet, defaultInformerResyncTime)

	nodeInformer := k8sInformerFactory.Core().V1().Nodes()
	return nodeInformer, k8sInformerFactory
}

func createEndpointFactoryAndInformer(cfg *rest.Config, namespace string) (
	v12.EndpointInformer, externalversions.SharedInformerFactory) {
	submarinerClientSet, err := versioned.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building submariner clientset: %s", err.Error())
	}

	submarinerInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(submarinerClientSet,
		defaultInformerResyncTime, externalversions.WithNamespace(namespace))

	endpointInformer := submarinerInformerFactory.Submariner().V1().Endpoints()
	return endpointInformer, submarinerInformerFactory
}

func buildRestConfig(masterURL string, kubeconfig string) *rest.Config {
	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}
	return cfg
}
