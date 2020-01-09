package main

import (
	"flag"
	"sync"

	"github.com/kelseyhightower/envconfig"

	"github.com/submariner-io/submariner/pkg/globalnet/controllers/ipam"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/signals"
)

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

	gatewayMonitor, err := ipam.NewGatewayMonitor(&ipamSpec, cfg, stopCh)
	if err != nil {
		klog.Fatalf("Error creating gatewayMonitor: %s", err.Error())
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		if err = gatewayMonitor.Run(stopCh); err != nil {
			klog.Fatalf("Error running gatewayMonitor: %s", err.Error())
		}
	}()

	wg.Wait()
	klog.Fatal("All controllers stopped or exited. Stopping main loop")
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
