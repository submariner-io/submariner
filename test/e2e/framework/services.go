package framework

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	TestAppLabel = "test-app"
)

func (f *Framework) CreateTCPService(cluster ClusterIndex, selectorName string, port int) *v1.Service {

	tcpService := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("test-svc-%s", selectorName),
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Port:       int32(port),
				TargetPort: intstr.FromInt(port),
				Protocol:   v1.ProtocolTCP,
			}},
			Selector: map[string]string{
				TestAppLabel: selectorName,
			},
		},
	}

	services := f.ClusterClients[cluster].CoreV1().Services(f.Namespace)

	return AwaitUntil("create service", func() (interface{}, error) {
		service, err := services.Create(&tcpService)
		if errors.IsAlreadyExists(err) {
			err = services.Delete(tcpService.Name, &metav1.DeleteOptions{})
			if err != nil {
				return nil, err
			}

			service, err = services.Create(&tcpService)
		}

		return service, err
	}, NoopCheckResult).(*v1.Service)
}

// AwaitServiceByAnnotation queries the service and looks for the presence of annotation.
func (f *Framework) AwaitServiceByAnnotation(cluster ClusterIndex, annotation string, svcName string, namespace string) *v1.Service {
	return AwaitUntil("get"+annotation+" annotation for service "+svcName, func() (interface{}, error) {
		service, err := f.ClusterClients[cluster].CoreV1().Services(namespace).Get(svcName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return service, err
	}, func(result interface{}) (bool, string, error) {
		if result == nil {
			return false, "No Service found", nil
		}

		svc := result.(*v1.Service)
		if svc.GetAnnotations()[annotation] == "" {
			return false, fmt.Sprintf("Service %q does not have annotation %q yet", svcName, annotation), nil
		}
		return true, "", nil
	}).(*v1.Service)
}
