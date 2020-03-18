package framework

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

const (
	// Polling interval while trying to create objects
	PollInterval = 100 * time.Millisecond
)

const (
	HostNetworking = true
	PodNetworking  = false
)

type RemoteEndpoint int

const (
	PodIP RemoteEndpoint = iota
	ServiceIP
	GlobalIP
)

type ClusterIndex int

const (
	ClusterA ClusterIndex = iota
	ClusterB
	ClusterC
)

const (
	SubmarinerEngine = "submariner-engine"
	GatewayLabel     = "submariner.io/gateway"
)

type PatchFunc func(pt types.PatchType, payload []byte) error

type PatchStringValue struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

type PatchUInt32Value struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value uint32 `json:"value"`
}

type DoOperationFunc func() (interface{}, error)
type CheckResultFunc func(result interface{}) (bool, string, error)

// Framework supports common operations used by e2e tests; it will keep a client & a namespace for you.
// Eventual goal is to merge this with integration test framework.
type Framework struct {
	BaseName string

	// Set together with creating the ClientSet and the namespace.
	// Guaranteed to be unique in the cluster even when running the same
	// test multiple times in parallel.
	UniqueName string

	// These are now set in TestContext but remain here as well for now for legacy.
	ClusterClients    []*kubeclientset.Clientset
	SubmarinerClients []*submarinerClientset.Clientset

	SkipNamespaceCreation    bool            // Whether to skip creating a namespace
	Namespace                string          // Every test has a namespace at least unless creation is skipped
	namespacesToDelete       map[string]bool // Some tests have more than one.
	NamespaceDeletionTimeout time.Duration

	// To make sure that this framework cleans up after itself, no matter what,
	// we install a Cleanup action before each test and clear it after.  If we
	// should abort, the AfterSuite hook should run all Cleanup actions.
	cleanupHandle CleanupActionHandle
}

// NewDefaultFramework makes a new framework and sets up a BeforeEach/AfterEach for
// you (you can write additional before/after each functions).
func NewDefaultFramework(baseName string) *Framework {
	return NewFramework(baseName)
}

// NewFramework creates a test framework.
func NewFramework(baseName string) *Framework {
	f := &Framework{
		BaseName:           baseName,
		namespacesToDelete: map[string]bool{},
	}

	ginkgo.BeforeEach(f.BeforeEach)
	ginkgo.AfterEach(f.AfterEach)

	return f
}

func BeforeSuite() {
	ginkgo.By("Creating kubernetes clients")

	if len(TestContext.KubeConfig) > 0 {
		Expect(len(TestContext.KubeConfigs)).To(BeZero(),
			"Either KubeConfig or KubeConfigs must be specified but not both")
		for _, context := range TestContext.KubeContexts {
			TestContext.ClusterClients = append(TestContext.ClusterClients, createKubernetesClient(TestContext.KubeConfig, context))
			TestContext.SubmarinerClients = append(TestContext.SubmarinerClients, createSubmarinerClient(TestContext.KubeConfig, context))
		}

		// if cluster IDs are not provided we assume that cluster-id == context
		if len(TestContext.ClusterIDs) == 0 {
			TestContext.ClusterIDs = TestContext.KubeContexts
		}

	} else if len(TestContext.KubeConfigs) > 0 {
		Expect(len(TestContext.KubeConfigs)).To(Equal(len(TestContext.ClusterIDs)),
			"One ClusterID must be provided for each item in the KubeConfigs")
		for _, kubeConfig := range TestContext.KubeConfigs {
			TestContext.ClusterClients = append(TestContext.ClusterClients, createKubernetesClient(kubeConfig, ""))
			TestContext.SubmarinerClients = append(TestContext.SubmarinerClients, createSubmarinerClient(kubeConfig, ""))
		}
	} else {
		ginkgo.Fail("One of KubeConfig or KubeConfigs must be specified")
	}

	queryAndUpdateGlobalnetStatus()
}

func queryAndUpdateGlobalnetStatus() {
	TestContext.GlobalnetEnabled = false
	clusterIndex := ClusterB
	clusterName := TestContext.KubeContexts[clusterIndex]
	clusters := TestContext.SubmarinerClients[clusterIndex].SubmarinerV1().Clusters(TestContext.SubmarinerNamespace)
	AwaitUntil("find the submariner Cluster for "+clusterName, func() (interface{}, error) {
		cluster, err := clusters.Get(clusterName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return cluster, err
	}, func(result interface{}) (bool, string, error) {
		if result == nil {
			return false, "No Cluster found", nil
		}

		cluster := result.(*submarinerv1.Cluster)
		if len(cluster.Spec.GlobalCIDR) != 0 {
			// Based on the status of GlobalnetEnabled, certain tests will be skipped/executed.
			TestContext.GlobalnetEnabled = true
		}

		return true, "", nil
	})
}

func (f *Framework) BeforeEach() {
	// workaround for a bug in ginkgo.
	// https://github.com/onsi/ginkgo/issues/222
	f.cleanupHandle = AddCleanupAction(f.AfterEach)

	f.ClusterClients = TestContext.ClusterClients
	f.SubmarinerClients = TestContext.SubmarinerClients

	if !f.SkipNamespaceCreation {
		ginkgo.By(fmt.Sprintf("Creating namespace objects with basename %q", f.BaseName))

		namespaceLabels := map[string]string{
			"e2e-framework": f.BaseName,
		}

		for idx, clientSet := range f.ClusterClients {
			switch ClusterIndex(idx) {
			case ClusterA: // On the first cluster we let k8s generate a name for the namespace
				namespace := generateNamespace(clientSet, f.BaseName, namespaceLabels)
				f.Namespace = namespace.GetName()
				f.UniqueName = namespace.GetName()
				f.AddNamespacesToDelete(namespace)
				ginkgo.By(fmt.Sprintf("Generated namespace %q in cluster %q to execute the tests in", f.Namespace, TestContext.KubeContexts[idx]))
			default: // On the other clusters we use the same name to make tracing easier
				ginkgo.By(fmt.Sprintf("Creating namespace %q in cluster %q", f.Namespace, TestContext.KubeContexts[idx]))
				f.CreateNamespace(clientSet, f.Namespace, namespaceLabels)
			}
		}
	} else {
		f.UniqueName = string(uuid.NewUUID())
	}

}

func createKubernetesClient(kubeConfig, context string) *kubeclientset.Clientset {
	restConfig := createRestConfig(kubeConfig, context)
	clientSet, err := kubeclientset.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred())

	// create scales getter, set GroupVersion and NegotiatedSerializer to default values
	// as they are required when creating a REST client.
	if restConfig.GroupVersion == nil {
		restConfig.GroupVersion = &schema.GroupVersion{}
	}
	if restConfig.NegotiatedSerializer == nil {
		restConfig.NegotiatedSerializer = scheme.Codecs
	}
	return clientSet
}

func createRestConfig(kubeConfig, context string) *rest.Config {
	restConfig, _, err := loadConfig(kubeConfig, context)
	if err != nil {
		Errorf("Unable to load kubeconfig file %s for context %s, this is a non-recoverable error",
			TestContext.KubeConfig, context)
		Errorf("loadConfig err: %s", err.Error())
		os.Exit(1)
	}
	testDesc := ginkgo.CurrentGinkgoTestDescription()
	if len(testDesc.ComponentTexts) > 0 {
		componentTexts := strings.Join(testDesc.ComponentTexts, " ")
		restConfig.UserAgent = fmt.Sprintf(
			"%v -- %v",
			rest.DefaultKubernetesUserAgent(),
			componentTexts)
	}
	restConfig.QPS = TestContext.ClientQPS
	restConfig.Burst = TestContext.ClientBurst
	if TestContext.GroupVersion != nil {
		restConfig.GroupVersion = TestContext.GroupVersion
	}
	return restConfig
}

func deleteNamespace(client kubeclientset.Interface, namespaceName string) error {
	return client.CoreV1().Namespaces().Delete(
		namespaceName,
		&metav1.DeleteOptions{})
}

// AfterEach deletes the namespace, after reading its events.
func (f *Framework) AfterEach() {
	RemoveCleanupAction(f.cleanupHandle)

	// DeleteNamespace at the very end in defer, to avoid any
	// expectation failures preventing deleting the namespace.
	defer func() {
		var nsDeletionErrors []error

		// Whether to delete namespace is determined by 3 factors: delete-namespace flag, delete-namespace-on-failure flag and the test result
		// if delete-namespace set to false, namespace will always be preserved.
		// if delete-namespace is true and delete-namespace-on-failure is false, namespace will be preserved if test failed.
		for ns := range f.namespacesToDelete {
			if err := f.deleteNamespaceFromAllClusters(ns); err != nil {
				nsDeletionErrors = append(nsDeletionErrors, err)
			}

			delete(f.namespacesToDelete, ns)
		}

		// Paranoia-- prevent reuse!
		f.Namespace = ""
		f.ClusterClients = nil

		// if we had errors deleting, report them now.
		if len(nsDeletionErrors) != 0 {
			Failf(k8serrors.NewAggregate(nsDeletionErrors).Error())
		}
	}()

}

func (f *Framework) deleteNamespaceFromAllClusters(ns string) error {
	var errs []error
	for i, clientSet := range f.ClusterClients {
		ginkgo.By(fmt.Sprintf("Deleting namespace %q on cluster %q", ns, TestContext.KubeContexts[i]))
		if err := deleteNamespace(clientSet, ns); err != nil {
			switch {
			case apierrors.IsNotFound(err):
				Logf("Namespace %q was already deleted", ns)
			case apierrors.IsConflict(err):
				Logf("Namespace %v scheduled for deletion, resources being purged", ns)
			default:
				errs = append(errs, errors.WithMessagef(err, "Failed to delete namespace %q on cluster %q", ns, TestContext.KubeContexts[i]))
			}
		}
	}

	return k8serrors.NewAggregate(errs)
}

// CreateNamespace creates a namespace for e2e testing.
func (f *Framework) CreateNamespace(clientSet *kubeclientset.Clientset,
	baseName string, labels map[string]string) *v1.Namespace {

	ns := createTestNamespace(clientSet, baseName, labels)
	f.AddNamespacesToDelete(ns)
	return ns
}

func (f *Framework) AddNamespacesToDelete(namespaces ...*v1.Namespace) {
	for _, ns := range namespaces {
		if ns == nil {
			continue
		}

		f.namespacesToDelete[ns.Name] = true
	}
}

func generateNamespace(client kubeclientset.Interface, baseName string, labels map[string]string) *v1.Namespace {
	namespaceObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("e2e-tests-%v-", baseName),
			Labels:       labels,
		},
	}

	namespace, err := client.CoreV1().Namespaces().Create(namespaceObj)
	Expect(err).NotTo(HaveOccurred(), "Error generating namespace %v", namespaceObj)
	return namespace
}

func createTestNamespace(client kubeclientset.Interface, name string, labels map[string]string) *v1.Namespace {
	namespace := createNamespace(client, name, labels)
	return namespace
}

func createNamespace(client kubeclientset.Interface, name string, labels map[string]string) *v1.Namespace {
	namespaceObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}

	namespace, err := client.CoreV1().Namespaces().Create(namespaceObj)
	Expect(err).NotTo(HaveOccurred(), "Error creating namespace %v", namespaceObj)
	return namespace
}

// PatchString performs a REST patch operation for the given path and string value.
func PatchString(path string, value string, patchFunc PatchFunc) {
	payload := []PatchStringValue{{
		Op:    "add",
		Path:  path,
		Value: value,
	}}

	doPatchOperation(payload, patchFunc)
}

// PatchInt performs a REST patch operation for the given path and int value.
func PatchInt(path string, value uint32, patchFunc PatchFunc) {
	payload := []PatchUInt32Value{{
		Op:    "add",
		Path:  path,
		Value: value,
	}}

	doPatchOperation(payload, patchFunc)
}

func doPatchOperation(payload interface{}, patchFunc PatchFunc) {
	payloadBytes, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())

	AwaitUntil("perform patch operation", func() (interface{}, error) {
		return nil, patchFunc(types.JSONPatchType, payloadBytes)
	}, NoopCheckResult)
}

func NoopCheckResult(interface{}) (bool, string, error) {
	return true, "", nil
}

// AwaitUntil periodically performs the given operation until the given CheckResultFunc returns true, an error, or a
// timeout is reached.
func AwaitUntil(opMsg string, doOperation DoOperationFunc, checkResult CheckResultFunc) interface{} {
	result, errMsg, err := AwaitResultOrError(opMsg, doOperation, checkResult)
	Expect(err).NotTo(HaveOccurred(), errMsg)
	return result
}

func AwaitResultOrError(opMsg string, doOperation DoOperationFunc, checkResult CheckResultFunc) (interface{}, string, error) {
	var finalResult interface{}
	var lastMsg string
	err := wait.PollImmediate(5*time.Second, time.Duration(TestContext.OperationTimeout)*time.Second, func() (bool, error) {
		result, err := doOperation()
		if err != nil {
			if IsTransientError(err, opMsg) {
				return false, nil
			}
			return false, err
		}

		ok, msg, err := checkResult(result)
		if err != nil {
			return false, err
		}

		if ok {
			finalResult = result
			return true, nil
		}

		lastMsg = msg
		return false, nil
	})

	errMsg := ""
	if err != nil {
		errMsg = "Failed to " + opMsg
		if lastMsg != "" {
			errMsg += ". " + lastMsg
		}
	}

	return finalResult, errMsg, err
}
