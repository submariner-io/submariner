package main

import (
	"context"
	"flag"
	"github.com/kelseyhightower/envconfig"
	"github.com/rancher/submariner/pkg/cableengine"
	"github.com/rancher/submariner/pkg/cableengine/ipsec"
	"github.com/rancher/submariner/pkg/controllers/datastoresyncer"
	"github.com/rancher/submariner/pkg/datastore"
	"github.com/rancher/submariner/pkg/types"
	"github.com/rancher/submariner/pkg/util"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	"os"
	"sync"
	"time"

	kubeInformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	submarinerClientset "github.com/rancher/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/rancher/submariner/pkg/client/informers/externalversions"
	subk8s "github.com/rancher/submariner/pkg/datastore/kubernetes"
	"github.com/rancher/submariner/pkg/controllers/tunnel"
	"github.com/rancher/submariner/pkg/datastore/phpapi"
	"github.com/rancher/submariner/pkg/signals"
)

var (
	localMasterUrl  string
	localKubeconfig string
)

func init() {
	flag.StringVar(&localKubeconfig, "kubeconfig", "", "Path to kubeconfig of local cluster. Only required if out-of-cluster.")
	flag.StringVar(&localMasterUrl, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	klog.V(2).Info("Starting submariner")
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	var ss types.SubmarinerSpecification
	err := envconfig.Process("submariner", &ss)
	if err != nil {
		klog.Fatal(err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags(localMasterUrl, localKubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	submarinerClient, err := submarinerClientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building submariner clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeInformers.NewSharedInformerFactoryWithOptions(kubeClient, time.Second*30, kubeInformers.WithNamespace(ss.Namespace))
	submarinerInformerFactory := submarinerInformers.NewSharedInformerFactoryWithOptions(submarinerClient, time.Second*30, submarinerInformers.WithNamespace(ss.Namespace))
	
	start := func(context.Context) {
		var ce cableengine.CableEngine
		localCluster, err := util.GetLocalCluster(ss)
		if err != nil {
			klog.Fatalf("Fatal error occurred while retrieving local cluster %v", localCluster)
		}
		localEndpoint, err := util.GetLocalEndpoint(ss.ClusterId, "ipsec", nil, ss.NatEnabled, append(ss.ServiceCidr, ss.ClusterCidr...))
		if err != nil {
			klog.Fatalf("Fatal error occurred while retrieving local endpoint %v", localCluster)
		}
		ce = ipsec.NewEngine(append(ss.ClusterCidr, ss.ServiceCidr...), localCluster, localEndpoint)

		tunnelController := tunnel.NewTunnelController(ss.Namespace, ce, kubeClient, submarinerClient,
			submarinerInformerFactory.Submariner().V1().Endpoints())

		var ds datastore.Datastore

		switch ss.Broker {
		case "phpapi":
			secure, err := util.ParseSecure(ss.Token)
			if err != nil {
				klog.Fatalf("Error parsing secure token: %v", err)
			}
			ds = phpapi.NewPHPAPI(secure.ApiKey)
		case "k8s":
			ds = subk8s.NewK8sDatastore(ss.ClusterId, stopCh)
		default:
			panic("No backend was specified")
		}
		klog.V(6).Infof("Creating new datastore syncer")
		dsSyncer := datastoresyncer.NewDatastoreSyncer(ss.ClusterId, ss.Namespace, kubeClient, submarinerClient, submarinerInformerFactory.Submariner().V1().Clusters(), submarinerInformerFactory.Submariner().V1().Endpoints(), ds, ss.ColorCodes, localCluster, localEndpoint)

		kubeInformerFactory.Start(stopCh)
		submarinerInformerFactory.Start(stopCh)

		klog.V(4).Infof("Starting controllers")
		var wg sync.WaitGroup
		wg.Add(3)

		go func() {
			defer wg.Done()
			ce.StartEngine(true)
		}()

		go func() {
			defer wg.Done()
			if err = tunnelController.Run(stopCh); err != nil {
				klog.Fatalf("Error running tunnel controller: %s", err.Error())
			}
		}()

		go func() {
			defer wg.Done()
			if err = dsSyncer.Run(stopCh); err != nil {
				klog.Fatalf("Error running datastoresyncer controller: %s", err.Error())
			}
		}()

		wg.Wait()
	}

	leClient, err := kubernetes.NewForConfig(rest.AddUserAgent(cfg, "leader-election"))
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.V(4).Infof)
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "submariner-controller"})
	if err != nil {
		klog.Fatal(err)
	}
	startLeaderElection(leClient, recorder, start)
	klog.Fatal("All controllers stopped or exited. Stopping main loop")
}

func startLeaderElection(leaderElectionClient kubernetes.Interface, recorder record.EventRecorder, run func(ctx context.Context)) {
	id, err := os.Hostname()
	if err != nil {
		klog.Fatalf("error getting hostname: %s", err.Error())
	}
	
	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)
	namespace, _, err := kubeconfig.Namespace()
	if err != nil {
		klog.Infof("Could not obtain a namespace to use for the leader election lock - the error was: %v. Using the default \"submariner\" namespace.", err)
		namespace = "submariner"
	} else {
		klog.Infof("Using namespace %s for the leader election lock", namespace)
	}

	// Lock required for leader election
	rl := resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "submariner-engine-lock",
		},
		Client: leaderElectionClient.CoreV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      id + "-submariner-engine",
			EventRecorder: recorder,
		},
	}

	leaderelection.RunOrDie(context.TODO(), leaderelection.LeaderElectionConfig{
		Lock:          &rl,
		LeaseDuration: 15*time.Second,
		RenewDeadline: 10*time.Second,
		RetryPeriod:   3*time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {
				klog.Fatalf("leaderelection lost")
			},
		},
	})
}
