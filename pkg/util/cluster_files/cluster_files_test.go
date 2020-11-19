package cluster_files_test

import (
	"io/ioutil"
	"testing"

	v1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/submariner-io/submariner/pkg/util/cluster_files"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

const theDataStr = "I'm the data"

var theData = []byte(theDataStr)

var _ = Describe("Cluster Files Get", func() {
	var client kubernetes.Interface
	BeforeEach(func() {
		client = fake.NewSimpleClientset(
			&v1.Secret{
				ObjectMeta: v1meta.ObjectMeta{Namespace: "ns1", Name: "my-secret"},
				Data: map[string][]byte{
					"data1": theData,
				},
			},
			&v1.ConfigMap{
				ObjectMeta: v1meta.ObjectMeta{Namespace: "ns1", Name: "my-configmap-binary"},
				BinaryData: map[string][]byte{
					"data1": theData,
				},
			},
			&v1.ConfigMap{
				ObjectMeta: v1meta.ObjectMeta{Namespace: "ns1", Name: "my-configmap"},
				Data: map[string]string{
					"data1": theDataStr,
				},
			})

	})

	When("The schema is unknown", func() {
		It("should return error", func() {
			_, err := cluster_files.Get(client, "randomschema://ns1/my-secret-noo/data1")
			Expect(err).To(Equal(cluster_files.UnsupportedSchema))
		})
	})

	When("The files don't exist", func() {
		It("should return error", func() {
			_, err := cluster_files.Get(client, "secret://ns1/my-secret-noo/data1")
			Expect(err).To(HaveOccurred())
			_, err = cluster_files.Get(client, "configmap://ns1/my-configmap-noo/data1")
			Expect(err).To(HaveOccurred())
		})
	})

	When("The content inside the file does not exist", func() {
		It("should return the data in a tmp file for a secret", func() {
			_, err := cluster_files.Get(client, "secret://ns1/my-secret/data1-does-not-exist")
			Expect(err).To(Equal(cluster_files.FileContentNotFound))
		})
	})

	When("The URL is malformed for this module", func() {
		It("should return malformed URL error", func() {
			_, err := cluster_files.Get(client, "secret://ns1/")
			Expect(err).To(Equal(cluster_files.MalformedURL))
		})
	})

	When("The URL is malformed for this module", func() {
		It("should return malformed URL error", func() {
			_, err := cluster_files.Get(client, "secret://ns1/secret-with-no-content-detail")
			Expect(err).To(Equal(cluster_files.MalformedURL))
		})
	})

	When("The files exist", func() {
		It("should return the data in a tmp file for a secret", func() {
			file, err := cluster_files.Get(client, "secret://ns1/my-secret/data1")
			Expect(err).NotTo(HaveOccurred())
			fileContent, err := ioutil.ReadFile(file)
			Expect(err).NotTo(HaveOccurred())
			Expect(fileContent).To(Equal(theData))
		})
		It("should return the data in a tmp file for a configmap", func() {
			file, err := cluster_files.Get(client, "configmap://ns1/my-configmap/data1")
			Expect(err).NotTo(HaveOccurred())
			fileContent, err := ioutil.ReadFile(file)
			Expect(err).NotTo(HaveOccurred())
			Expect(fileContent).To(Equal(theData))
		})
		It("should return the data in a tmp file for a configmap with binary data", func() {
			file, err := cluster_files.Get(client, "configmap://ns1/my-configmap-binary/data1")
			Expect(err).NotTo(HaveOccurred())
			fileContent, err := ioutil.ReadFile(file)
			Expect(err).NotTo(HaveOccurred())
			Expect(fileContent).To(Equal(theData))
		})

	})

	It("should return the original path for the file:/// scheme", func() {
		file, err := cluster_files.Get(nil, "file:///dir/file")
		Expect(err).NotTo(HaveOccurred())
		Expect(file).To(Equal("/dir/file"))
	})
})

func TestUtil(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster Files Suite")
}
