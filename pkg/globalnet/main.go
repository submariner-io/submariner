package main

import (
	"flag"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	"github.com/submariner-io/submariner/pkg/globalnet/controllers/ipam"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/signals"
)

const defaultResync = 60 * time.Second

var (
	masterURL  string
	kubeconfig string
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	var ipamSpec ipam.SubmarinerIpamControllerSpecification

	err := envconfig.Process("submariner", &ipamSpec)
	if err != nil {
		klog.Fatal(err)
	}
	klog.V(2).Info("Starting submariner-globalnet", ipamSpec)

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building clientset: %s", err.Error())
	}

	informerFactory := informers.NewSharedInformerFactoryWithOptions(clientSet, defaultResync)

	informerConfig := ipam.InformerConfigStruct{
		KubeClientSet:   clientSet,
		ServiceInformer: informerFactory.Core().V1().Services(),
		PodInformer:     informerFactory.Core().V1().Pods(),
	}

	ipamController, err := ipam.NewController(&ipamSpec, &informerConfig)
	if err != nil {
		klog.Fatalf("Error creating controller: %s", err.Error())
	}

	informerFactory.Start(stopCh)

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		if err = ipamController.Run(stopCh); err != nil {
			klog.Fatalf("Error running ipam controller: %s", err.Error())
		}
	}()

	wg.Wait()
	klog.Fatal("All controllers stopped or exited. Stopping main loop")
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
