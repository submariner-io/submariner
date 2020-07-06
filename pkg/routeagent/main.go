package main

import (
	"flag"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	"github.com/submariner-io/submariner/pkg/routeagent/controllers/route"
	"github.com/submariner-io/submariner/pkg/util"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var (
	masterURL  string
	kubeconfig string
)

type SubmarinerRouteControllerSpecification struct {
	ClusterID   string
	Namespace   string
	ClusterCidr []string
	ServiceCidr []string
}

func filterRouteAgentPods(options *v1.ListOptions) {
	options.LabelSelector = route.SmRouteAgentFilter
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	var srcs SubmarinerRouteControllerSpecification

	err := envconfig.Process("submariner", &srcs)
	if err != nil {
		klog.Fatal(err)
	}

	klog.Info("Starting submariner-route-agent")
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

	submarinerInformerFactory := submarinerInformers.NewSharedInformerFactoryWithOptions(submarinerClient, time.Second*30,
		submarinerInformers.WithNamespace(srcs.Namespace))

	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building clientset: %s", err.Error())
	}

	informerFactory := informers.NewSharedInformerFactoryWithOptions(clientSet, time.Second*60, informers.WithNamespace(srcs.Namespace),
		informers.WithTweakListOptions(filterRouteAgentPods))

	informerConfig := route.InformerConfigStruct{
		SubmarinerClientSet: submarinerClient,
		ClientSet:           clientSet,
		EndpointInformer:    submarinerInformerFactory.Submariner().V1().Endpoints(),
		PodInformer:         informerFactory.Core().V1().Pods(),
	}

	defLink, err := util.GetDefaultGatewayInterface()
	if err != nil {
		klog.Errorf("Unable to find the default interface on host: %s", err.Error())
		return
	}

	routeController := route.NewController(srcs.ClusterID, srcs.ClusterCidr, srcs.ServiceCidr, srcs.Namespace, defLink, informerConfig)

	submarinerInformerFactory.Start(stopCh)
	informerFactory.Start(stopCh)

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		if err = routeController.Run(stopCh); err != nil {
			klog.Fatalf("Error running route controller: %s", err.Error())
		}
	}()

	wg.Wait()
	klog.Info("All controllers stopped or exited. Stopping main loop")
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
