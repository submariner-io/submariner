package submariner

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/intstr"

	submarinerv1alpha1 "github.com/submariner-operator/submariner-operator/pkg/apis/submariner/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_submariner")

// Add creates a new Submariner Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileSubmariner{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("submariner-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Submariner
	err = c.Watch(&source.Kind{Type: &submarinerv1alpha1.Submariner{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Pods and requeue the owner Submariner
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &submarinerv1alpha1.Submariner{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileSubmariner implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileSubmariner{}

// ReconcileSubmariner reconciles a Submariner object
type ReconcileSubmariner struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Submariner object and makes changes based on the state read
// and what is in the Submariner.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileSubmariner) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Submariner")

	// Fetch the Submariner instance
	instance := &submarinerv1alpha1.Submariner{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Create submariner-engine SA
	//subm_engine_sa := corev1.ServiceAccount{}
	//subm_engine_sa.Name = "submariner-engine"
	//reqLogger.Info("Created a new SA", "SA.Name", subm_engine_sa.Name)

	if err = r.reconcileEngineDeployment(instance, reqLogger); err != nil {
		return reconcile.Result{}, err
	}

	if err = r.reconcileRouteagentDaemonSet(instance, reqLogger); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileSubmariner) reconcileEngineDeployment(instance *submarinerv1alpha1.Submariner, reqLogger logr.Logger) error {

	var err error

	deployment := newDeploymentForCR(instance)
	// Set Submariner instance as the owner and controller
	if err = controllerutil.SetControllerReference(instance, deployment, r.scheme); err != nil {
		return err
	}
	foundDeployment := &appsv1.Deployment{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, foundDeployment)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new Deployment",
			"Deployment.Namespace", deployment.Namespace, "Deployment.Name", deployment.Name)
		err = r.client.Create(context.TODO(), deployment)
		if err != nil {
			return err
		}

		return nil
	} else if err != nil {
		return err
	}
	reqLogger.Info("Skip reconcile: Deployment already exists",
		"Deployment.Namespace", foundDeployment.Namespace, "Deployment.Name", foundDeployment.Name)
	return nil
}

func (r *ReconcileSubmariner) reconcileRouteagentDaemonSet(instance *submarinerv1alpha1.Submariner, reqLogger logr.Logger) error {

	var err error

	daemonSet := newRouteAgentDaemonSet(instance)

	// Set Routeagent instance as the owner and controller
	if err = controllerutil.SetControllerReference(instance, daemonSet, r.scheme); err != nil {
		return err
	}

	foundDaemonSet := &appsv1.DaemonSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: daemonSet.Name, Namespace: daemonSet.Namespace}, foundDaemonSet)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new DaemonSet", "DaemonSet.Namespace", daemonSet.Namespace, "DaemonSet.Name", daemonSet.Name)
		if err = r.client.Create(context.TODO(), daemonSet); err != nil {
			return err
		}
		return nil
	} else if err != nil {
		return err
	}

	reqLogger.Info("Skip reconcile: DaemonSet already exists",
		"DaemonSet.Namespace", foundDaemonSet.Namespace, "DaemonSet.Name", foundDaemonSet.Name)
	return nil
}

func newDeploymentForCR(cr *submarinerv1alpha1.Submariner) *appsv1.Deployment {

	labels := map[string]string{
		"app":       "submariner-engine",
		"component": "engine",
	}

	replicas := int32(1)
	revisionHistoryLimit := int32(5)
	progressDeadlineSeconds := int32(600)

	maxSurge := intstr.FromInt(1)
	maxUnavailable := intstr.FromInt(0)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Labels:    labels,
			Namespace: cr.Namespace,
			Name:      "submariner",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "submariner-engine"}},
			Template: newPodTemplateForCR(cr),
			Strategy: appsv1.DeploymentStrategy{
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxSurge:       &maxSurge,
					MaxUnavailable: &maxUnavailable,
				},
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
			RevisionHistoryLimit:    &revisionHistoryLimit,
			ProgressDeadlineSeconds: &progressDeadlineSeconds,
		},
	}

	return deployment
}

// newPodForCR returns a submariner pod with the same fields as the cr
func newPodTemplateForCR(cr *submarinerv1alpha1.Submariner) corev1.PodTemplateSpec {
	labels := map[string]string{
		"app": "submariner-engine",
	}

	// Create privilaged security context for Engine pod
	// FIXME: Seems like these have to be a var, so can pass pointer to bool var to SecurityContext. Cleaner option?
	allowPrivilegeEscalation := true
	privileged := true
	runAsNonRoot := false
	readOnlyRootFilesystem := false

	security_context_all_caps_privilaged := corev1.SecurityContext{
		Capabilities:             &corev1.Capabilities{Add: []corev1.Capability{"ALL"}},
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		Privileged:               &privileged,
		ReadOnlyRootFilesystem:   &readOnlyRootFilesystem,
		RunAsNonRoot:             &runAsNonRoot}

	// Create Pod
	terminationGracePeriodSeconds := int64(0)
	podTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: labels,
						},
						TopologyKey: "kubernetes.io/hostname",
					}},
				},
			},
			NodeSelector: map[string]string{"submariner.io/gateway": "true"},
			Containers: []corev1.Container{
				{
					Name:            "submariner",
					Image:           getImagePath(cr, engineImage),
					Command:         []string{"submariner.sh"},
					SecurityContext: &security_context_all_caps_privilaged,
					Env: []corev1.EnvVar{
						{Name: "SUBMARINER_NAMESPACE", Value: cr.Spec.Namespace},
						{Name: "SUBMARINER_CLUSTERCIDR", Value: cr.Spec.ClusterCIDR},
						{Name: "SUBMARINER_SERVICECIDR", Value: cr.Spec.ServiceCIDR},
						{Name: "SUBMARINER_CLUSTERID", Value: cr.Spec.ClusterID},
						{Name: "SUBMARINER_COLORCODES", Value: cr.Spec.ColorCodes},
						{Name: "SUBMARINER_DEBUG", Value: strconv.FormatBool(cr.Spec.Debug)},
						{Name: "SUBMARINER_NATENABLED", Value: strconv.FormatBool(cr.Spec.NatEnabled)},
						{Name: "SUBMARINER_BROKER", Value: cr.Spec.Broker},
						{Name: "BROKER_K8S_APISERVER", Value: cr.Spec.BrokerK8sApiServer},
						{Name: "BROKER_K8S_APISERVERTOKEN", Value: cr.Spec.BrokerK8sApiServerToken},
						{Name: "BROKER_K8S_REMOTENAMESPACE", Value: cr.Spec.BrokerK8sRemoteNamespace},
						{Name: "BROKER_K8S_CA", Value: cr.Spec.BrokerK8sCA},
						{Name: "CE_IPSEC_PSK", Value: cr.Spec.CeIPSecPSK},
						{Name: "CE_IPSEC_DEBUG", Value: strconv.FormatBool(cr.Spec.CeIPSecDebug)},
					},
				},
			},
			// TODO: Use SA submariner-engine or submariner?
			ServiceAccountName:            "submariner-operator",
			HostNetwork:                   true,
			TerminationGracePeriodSeconds: &terminationGracePeriodSeconds,
			RestartPolicy:                 corev1.RestartPolicyAlways,
			DNSPolicy:                     corev1.DNSClusterFirst,
		},
	}
	if cr.Spec.CeIPSecIKEPort != 0 {
		podTemplate.Spec.Containers[0].Env = append(podTemplate.Spec.Containers[0].Env,
			corev1.EnvVar{Name: "CE_IPSEC_IKEPORT", Value: strconv.Itoa(cr.Spec.CeIPSecIKEPort)})
	}

	if cr.Spec.CeIPSecNATTPort != 0 {
		podTemplate.Spec.Containers[0].Env = append(podTemplate.Spec.Containers[0].Env,
			corev1.EnvVar{Name: "CE_IPSEC_NATTPORT", Value: strconv.Itoa(cr.Spec.CeIPSecNATTPort)})
	}

	return podTemplate
}

func newRouteAgentDaemonSet(cr *submarinerv1alpha1.Submariner) *appsv1.DaemonSet {
	labels := map[string]string{
		"app":       "submariner-routeagent",
		"component": "routeagent",
	}

	matchLabels := map[string]string{
		"app": "submariner-routeagent",
	}

	allowPrivilegeEscalation := true
	privileged := true
	readOnlyFileSystem := false
	runAsNonRoot := false
	security_context_all_cap_allow_escal := corev1.SecurityContext{
		Capabilities:             &corev1.Capabilities{Add: []corev1.Capability{"ALL"}},
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		Privileged:               &privileged,
		ReadOnlyRootFilesystem:   &readOnlyFileSystem,
		RunAsNonRoot:             &runAsNonRoot,
	}

	terminationGracePeriodSeconds := int64(0)

	routeAgentDaemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cr.Namespace,
			Name:      "submariner-routeagent",
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: matchLabels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: &terminationGracePeriodSeconds,
					Containers: []corev1.Container{
						{
							Name:  "submariner-routeagent",
							Image: getImagePath(cr, routeAgentImage),
							// FIXME: Should be entrypoint script, find/use correct file for routeagent
							Command:         []string{"submariner-route-agent.sh"},
							SecurityContext: &security_context_all_cap_allow_escal,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "host-slash", MountPath: "/host", ReadOnly: true},
							},
							Env: []corev1.EnvVar{
								{Name: "SUBMARINER_NAMESPACE", Value: cr.Spec.Namespace},
								{Name: "SUBMARINER_CLUSTERID", Value: cr.Spec.ClusterID},
								{Name: "SUBMARINER_DEBUG", Value: strconv.FormatBool(cr.Spec.Debug)},
								{Name: "SUBMARINER_CLUSTERCIDR", Value: cr.Spec.ClusterCIDR},
								{Name: "SUBMARINER_SERVICECIDR", Value: cr.Spec.ServiceCIDR},
							},
						},
					},
					// TODO: Use SA submariner-routeagent or submariner?
					ServiceAccountName: "submariner-operator",
					HostNetwork:        true,
					Volumes: []corev1.Volume{
						{Name: "host-slash", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}},
					},
				},
			},
		},
	}

	return routeAgentDaemonSet
}

//TODO: move to a method on the API definitions, as the example shown by the etcd operator here :
//      https://github.com/coreos/etcd-operator/blob/8347d27afa18b6c76d4a8bb85ad56a2e60927018/pkg/apis/etcd/v1beta2/cluster.go#L185
func setSubmarinerDefaults(submariner *submarinerv1alpha1.Submariner) {

	spec := submariner.Spec
	if spec.Repository == "" {
		// An empty field is converted to the default upstream submariner repository where all images live
		spec.Repository = "quay.io/submariner"
	}

	if spec.Version == "" {
		spec.Version = "0.0.2"
	}
}

const (
	routeAgentImage = "submariner-route-agent"
	engineImage     = "submariner"
)

func getImagePath(submariner *submarinerv1alpha1.Submariner, componentImage string) string {
	var path string
	spec := submariner.Spec

	// If the repository is "local" we don't append it on the front of the image,
	// a local repository is used for development, testing and CI when we inject
	// images in the cluster, for example submariner:local, or submariner-route-agent:local
	if spec.Repository == "local" {
		path = componentImage
	} else {
		path = fmt.Sprintf("%s/%s", spec.Repository, componentImage)
	}

	path = fmt.Sprintf("%s:%s", path, spec.Version)
	return path
}
