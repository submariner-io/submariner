/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package clusterfiles_test

import (
	"io/ioutil"
	"testing"

	v1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/submariner-io/submariner/pkg/util/clusterfiles"

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

	When("The scheme is unknown", func() {
		It("should return an error", func() {
			_, err := clusterfiles.Get(client, "randomschema://ns1/my-secret-noo/data1")
			Expect(err).To(HaveOccurred())
		})
	})

	When("a file source does not exist", func() {
		It("should return an error", func() {
			_, err := clusterfiles.Get(client, "secret://ns1/my-secret-noo/data1")
			Expect(err).To(HaveOccurred())
			_, err = clusterfiles.Get(client, "configmap://ns1/my-configmap-noo/data1")
			Expect(err).To(HaveOccurred())
		})
	})

	When("the content inside the file does not exist", func() {
		It("should return an error", func() {
			_, err := clusterfiles.Get(client, "secret://ns1/my-secret/data1-does-not-exist")
			Expect(err).To(HaveOccurred())
		})
	})

	When("the URL is malformed", func() {
		It("should return an error", func() {
			_, err := clusterfiles.Get(client, "secret://ns1/")
			Expect(err).To(HaveOccurred())
			_, err = clusterfiles.Get(client, "secret://ns1/secret-with-no-content-detail")
			Expect(err).To(HaveOccurred())
		})
	})

	When("the source secret exist", func() {
		It("should return the data in a tmp file", func() {
			file, err := clusterfiles.Get(client, "secret://ns1/my-secret/data1")
			Expect(err).NotTo(HaveOccurred())
			fileContent, err := ioutil.ReadFile(file)
			Expect(err).NotTo(HaveOccurred())
			Expect(fileContent).To(Equal(theData))
		})
	})

	When("the source configmap exist", func() {
		It("should return the data in a tmp file", func() {
			file, err := clusterfiles.Get(client, "configmap://ns1/my-configmap/data1")
			Expect(err).NotTo(HaveOccurred())
			fileContent, err := ioutil.ReadFile(file)
			Expect(err).NotTo(HaveOccurred())
			Expect(fileContent).To(Equal(theData))
		})
	})

	When("the source configmap exist and has binary data", func() {
		It("should return the data in a tmp file", func() {
			file, err := clusterfiles.Get(client, "configmap://ns1/my-configmap-binary/data1")
			Expect(err).NotTo(HaveOccurred())
			fileContent, err := ioutil.ReadFile(file)
			Expect(err).NotTo(HaveOccurred())
			Expect(fileContent).To(Equal(theData))
		})

	})

	When("the source is a file", func() {
		It("should return the original path for the file:/// scheme", func() {
			file, err := clusterfiles.Get(nil, "file:///dir/file")
			Expect(err).NotTo(HaveOccurred())
			Expect(file).To(Equal("/dir/file"))
		})
	})
})

func TestClusterFiles(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster Files Suite")
}
