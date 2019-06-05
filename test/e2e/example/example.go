package example

import (
    "fmt"
    "time"

    "k8s.io/apimachinery/pkg/api/errors"
    "k8s.io/client-go/kubernetes"

    "github.com/rancher/submariner/test/e2e/framework"

    v1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/util/wait"

    . "github.com/onsi/ginkgo"
    . "github.com/onsi/gomega"
)

var _ = Describe("[example] Basic example to demonstrate how to write tests using the framework", func() {
    f := framework.NewDefaultFramework("basic-example")
    It("Should be able to list existing nodes on the cluster", func() {
        testListingNodes(f)
    })
    It("Should be able to create a pod using the provided client", func() {
        testCreatingAPod(f)
    })
})

func testListingNodes(f *framework.Framework) {
    for _, cs := range f.ClusterClients {
        testListingNodesFromCluster(cs)
    }
}

func testListingNodesFromCluster(cs *kubernetes.Clientset) {
    nc := cs.CoreV1().Nodes()
    By("Requesting node list from API")
    nodes, err := nc.List(metav1.ListOptions{})
    Expect(err).NotTo(HaveOccurred())
    By("Checking that we had more than 0 nodes on the reponse")
    Expect(len(nodes.Items)).ToNot(BeZero())
    for _, node := range nodes.Items {
        inIP, err := getIP(v1.NodeInternalIP, &node)
        Expect(err).NotTo(HaveOccurred())
        framework.Logf("Detected node with IP: %v", inIP)
    }
}

var (
    testPod = v1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            GenerateName: "example-pod",
            Labels: map[string]string{
                "example-pod": "",
            },
        },
        Spec: v1.PodSpec{
            Containers: []v1.Container{
                {
                    Name:  "example-pod",
                    Image: "busybox",
                    Command:  []string{"sh", "-c", "echo Hello Kubernetes, I am at $POD_IP! && sleep 3600"},
                    Env: []v1.EnvVar{
                        {
                            Name:      "POD_IP",
                            ValueFrom: &v1.EnvVarSource{FieldRef: &v1.ObjectFieldSelector{FieldPath: "status.podIP"}},
                        },
                    },
                },
            },
        },
    }
)

func testCreatingAPod(f *framework.Framework) {
    for _, cs := range f.ClusterClients {
        testCreatingAPodInCluster(cs, f)
    }
}

func testCreatingAPodInCluster(cs *kubernetes.Clientset, f *framework.Framework) {
    pc := cs.CoreV1().Pods(f.Namespace)
    By("Creating a bunch of pods")
    for i := 0; i < 3; i++ {
        _, err := pc.Create(&testPod)
        Expect(err).NotTo(HaveOccurred())
    }
    By("Waiting for the example-pod(s) to be scheduled and running")
    err := wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
        pods, err := pc.List(metav1.ListOptions{LabelSelector: "example-pod"})
        if err != nil {
            if errors.IsUnexpectedServerError(err) {
                framework.Logf("Transient failure when attempting to list pods: %v", err)
                return false, nil // return nil to avoid PollImmediate from stopping
            }
            return false, err
        }

        // check all pods are running
        for _, pod := range pods.Items {
            if pod.Status.Phase != v1.PodRunning {
                if pod.Status.Phase != v1.PodPending {
                    return false, fmt.Errorf("expected pod to be in phase \"Pending\" or \"Running\"")
                }
                return false, nil // pod is still pending
            }
        }
        return true, nil // all pods are running
    })
    Expect(err).NotTo(HaveOccurred())
    By("Collecting pod ClusterIPs just for fun")
    pods, err := pc.List(metav1.ListOptions{LabelSelector: "example-pod"})
    Expect(err).NotTo(HaveOccurred())
    for _, pod := range pods.Items {
        framework.Logf("Detected pod with IP: %v", pod.Status.PodIP)
    }
}

func getIP(iptype v1.NodeAddressType, node *v1.Node) (string, error) {
    for _, addr := range node.Status.Addresses {
        if addr.Type == iptype {
            return addr.Address, nil
        }
    }
    return "", fmt.Errorf("did not find %s on Node", iptype)
}
