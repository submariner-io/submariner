package framework

import (
    "fmt"

    . "github.com/onsi/gomega"
    v1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/util/intstr"
)

const (
    TestAppLabel = "test-app"
)


func (f *Framework) CreateTCPService(cluster int, selectorName string, port int) *v1.Service {

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

    kube := f.ClusterClients[cluster]
    services := kube.CoreV1().Services(f.Namespace)

    service, err :=services.Create(&tcpService)
    Expect(err).NotTo(HaveOccurred())

    return service
}
