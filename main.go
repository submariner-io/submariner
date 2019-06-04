package main

import (
	"context"
	"flag"
	"os"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/rancher/submariner/pkg/cableengine/ipsec"
	"github.com/rancher/submariner/pkg/controllers/datastoresyncer"
	"github.com/rancher/submariner/pkg/datastore"
	"github.com/rancher/submariner/pkg/types"
	"github.com/rancher/submariner/pkg/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"

	kubeInformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	submarinerClientset "github.com/rancher/submariner/pkg/client/clientset/versioned"
	submarinerInformers "github.com/rancher/submariner/pkg/client/informers/externalversions"
	"github.com/rancher/submariner/pkg/controllers/tunnel"
	subk8s "github.com/rancher/submariner/pkg/datastore/kubernetes"
	"github.com/rancher/submariner/pkg/datastore/phpapi"
	"github.com/rancher/submariner/pkg/signals"
)

var (
	localMasterURL  string
	localKubeconfig string
)

func init() {
	flag.StringVar(&localKubeconfig, "kubeconfig", "", "Path to kubeconfig of local cluster. Only required if out-of-cluster.")
	flag.StringVar(&localMasterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	klog.V(2).Info("Starting submariner")
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	var submSpec types.SubmarinerSpecification
	err := envconfig.Process("submariner", &submSpec)
	if err != nil {
		klog.Fatal(err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags(localMasterURL, localKubeconfig)
	if err != nil {
		klog.Exitf("Error building kubeconfig: %s", err.Error())
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Exitf("Error building kubernetes clientset: %s", err.Error())
	}

	submarinerClient, err := submarinerClientset.NewForConfig(cfg)
	if err != nil {
		klog.Exitf("Error building submariner clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeInformers.NewSharedInformerFactoryWithOptions(kubeClient, time.Second*30,
		kubeInformers.WithNamespace(submSpec.Namespace))
	submarinerInformerFactory := submarinerInformers.NewSharedInformerFactoryWithOptions(submarinerClient, time.Second*30,
		submarinerInformers.WithNamespace(submSpec.Namespace))

	start := func(context.Context) {
		localCluster, err := util.GetLocalCluster(submSpec)
		if err != nil {
			klog.Fatalf("Fatal error occurred while retrieving local cluster from %#v: %v", submSpec, err)
		}

		localEndpoint, err := util.GetLocalEndpoint(submSpec.ClusterID, "ipsec", nil, submSpec.NatEnabled,
			append(submSpec.ServiceCidr, submSpec.ClusterCidr...))

		if err != nil {
			klog.Fatalf("Fatal error occurred while retrieving local endpoint from %#v: %v", submSpec, err)
		}

		cableEngine, err := ipsec.NewEngine(append(submSpec.ClusterCidr, submSpec.ServiceCidr...), localCluster, localEndpoint)
		if err != nil {
			klog.Fatalf("Fatal error occurred creating ipsec engine: %v", err)
		}

		tunnelController := tunnel.NewController(submSpec.Namespace, cableEngine, kubeClient, submarinerClient,
			submarinerInformerFactory.Submariner().V1().Endpoints())

		var datastore datastore.Datastore
		switch submSpec.Broker {
		case "phpapi":
			secure, err := util.ParseSecure(submSpec.Token)
			if err != nil {
				klog.Fatalf("Error parsing secure token: %v", err)
			}

			datastore = phpapi.NewPHPAPI(secure.APIKey)
		case "k8s":
			datastore, err = subk8s.NewDatastore(submSpec.ClusterID, stopCh)
			if err != nil {
				klog.Fatalf("Error creating kubernetes datastore: %v", err)
			}
		default:
			klog.Fatalf("Invalid backend '%s' was specified", submSpec.Broker)
		}

		klog.V(6).Infof("Creating new datastore syncer")
		dsSyncer := datastoresyncer.NewDatastoreSyncer(submSpec.ClusterID, submSpec.Namespace, kubeClient, submarinerClient,
			submarinerInformerFactory.Submariner().V1().Clusters(), submarinerInformerFactory.Submariner().V1().Endpoints(), datastore,
			submSpec.ColorCodes, localCluster, localEndpoint)

		kubeInformerFactory.Start(stopCh)
		submarinerInformerFactory.Start(stopCh)

		klog.V(4).Infof("Starting controllers")

		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			if err = cableEngine.StartEngine(true); err != nil {
				klog.Fatalf("Error starting the cable engine: %v", err)
			}
		}()

		go func() {
			defer wg.Done()
			if err = tunnelController.Run(stopCh); err != nil {
				klog.Fatalf("Error running tunnel controller: %v", err)
			}
		}()

		go func() {
			defer wg.Done()
			if err = dsSyncer.Run(stopCh); err != nil {
				klog.Fatalf("Error running datastoresyncer controller: %v", err)
			}
		}()

		wg.Wait()
	}

	leClient, err := kubernetes.NewForConfig(rest.AddUserAgent(cfg, "leader-election"))
	if err != nil {
		klog.Fatal(err)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.V(4).Infof)
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "submariner-controller"})

	startLeaderElection(leClient, recorder, start)
	klog.Fatal("All controllers stopped or exited. Stopping main loop")
}

func startLeaderElection(leaderElectionClient kubernetes.Interface, recorder record.EventRecorder, run func(ctx context.Context)) {
	id, err := os.Hostname()
	if err != nil {
		klog.Fatalf("error getting hostname: %v", err)
	}

	// Lock required for leader election
	rl := resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Namespace: "submariner",
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
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   3 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {
				klog.Fatalf("leaderelection lost")
			},
		},
	})
}
