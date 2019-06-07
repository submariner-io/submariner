package main

import (
	"flag"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/rancher/submariner/pkg/routeagent/controllers/route"
	"github.com/rancher/submariner/pkg/util"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	submarinerClientset "github.com/rancher/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/rancher/submariner/pkg/client/informers/externalversions"
	"github.com/rancher/submariner/pkg/signals"
)

var (
	masterURL  string
	kubeconfig string
)

type SubmarinerRouteControllerSpecification struct {
	ClusterID string
	Namespace string
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	var srcs SubmarinerRouteControllerSpecification

	err := envconfig.Process("submariner", &srcs)
	if err != nil {
		klog.Fatal(err)
	}

	klog.V(2).Info("Starting submariner-route-agent")
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	submarinerClient, err := submarinerClientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building submariner clientset: %s", err.Error())
	}

	submarinerInformerFactory := submarinerInformers.NewSharedInformerFactoryWithOptions(submarinerClient, time.Second*30, submarinerInformers.WithNamespace(srcs.Namespace))

	defLink, err := util.GetDefaultGatewayInterface()
	routeController := route.NewController(srcs.ClusterID, srcs.Namespace, defLink, submarinerClient, submarinerInformerFactory.Submariner().V1().Clusters(), submarinerInformerFactory.Submariner().V1().Endpoints())

	submarinerInformerFactory.Start(stopCh)

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		if err = routeController.Run(stopCh); err != nil {
			klog.Fatalf("Error running route controller: %s", err.Error())
		}
	}()

	wg.Wait()
	klog.Fatal("All controllers stopped or exited. Stopping main loop")
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
