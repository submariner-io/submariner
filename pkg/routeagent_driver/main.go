package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/controller"
	"github.com/submariner-io/submariner/pkg/event/logger"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cni_interface"
	kp_iptables "github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy_iptables"
)

var (
	masterURL  string
	kubeconfig string
)

type specification struct {
	ClusterID   string
	Namespace   string
	ClusterCidr []string
	ServiceCidr []string
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	klog.Info("Starting submariner-route-agent using the event framework")
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	var env specification
	err := envconfig.Process("submariner", &env)
	if err != nil {
		klog.Fatalf("Error reading the environment variables: %s", err.Error())
	}

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	smClientset, err := submarinerClientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building submariner clientset: %s", err.Error())
	}

	if err = annotateNode(env.ClusterCidr, cfg); err != nil {
		klog.Errorf("Error while annotating the node: %s", err.Error())
	}

	np := os.Getenv("SUBMARINER_NETWORKPLUGIN")
	if np == "" {
		np = "generic"
	}

	registry := event.NewRegistry("routeagent_driver", np)
	if err := registry.AddHandlers(
		logger.NewHandler(),
		kp_iptables.NewSyncHandler(env.ClusterCidr, env.ServiceCidr, smClientset),
	); err != nil {
		klog.Fatalf("Error registering the handlers: %s", err.Error())
	}

	ctl, err := controller.New(&controller.Config{
		Registry:   registry,
		MasterURL:  masterURL,
		Kubeconfig: kubeconfig})

	if err != nil {
		klog.Fatalf("Error creating controller for event handling %v", err)
	}

	err = ctl.Start(stopCh)
	if err != nil {
		klog.Fatalf("Error starting controller: %v", err)
	}

	<-stopCh
	ctl.Stop()

	klog.Info("All controllers stopped or exited. Stopping submariner-networkplugin-syncer")
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func annotateNode(clusterCidr []string, cfg *restclient.Config) error {
	k8sClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building clientset: %s", err.Error())
	}

	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		return fmt.Errorf("Error reading the NODE_NAME from the environment")
	}

	err = cni_interface.AnnotateNodeWithCNIInterfaceIP(nodeName, k8sClientSet, clusterCidr)
	if err != nil {
		return fmt.Errorf("AnnotateNodeWithCNIInterfaceIP returned error %v", err)
	}

	return nil
}
