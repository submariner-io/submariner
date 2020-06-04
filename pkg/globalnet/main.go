package main

import (
	"flag"
	"os"
	"sync"

	"github.com/kelseyhightower/envconfig"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/ipam"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
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
	klog.Info("Starting submariner-globalnet", ipamSpec)

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	submarinerClient, err := submarinerClientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("error building submariner clientset: %s", err.Error())
	}

	localCluster, err := submarinerClient.SubmarinerV1().Clusters(ipamSpec.Namespace).Get(ipamSpec.ClusterID, metav1.GetOptions{})
	if err != nil {
		klog.Fatalf("error while retrieving the local cluster %q info: %v", ipamSpec.ClusterID, err)
	}

	if localCluster.Spec.GlobalCIDR != nil && len(localCluster.Spec.GlobalCIDR) > 0 {
		// TODO: Revisit when support for more than one globalCIDR is implemented.
		ipamSpec.GlobalCIDR = localCluster.Spec.GlobalCIDR
	} else {
		klog.Errorf("Cluster %s is not configured to use globalCidr", ipamSpec.ClusterID)
		os.Exit(1)
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
	klog.Infof("All controllers stopped or exited. Stopping main loop")
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
