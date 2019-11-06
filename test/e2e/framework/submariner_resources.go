package framework

import (
	. "github.com/onsi/gomega"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
)

func (f *Framework) createSubmarinerClient(context string) *submarinerClientset.Clientset {
	restConfig := f.createRestConfig(context)
	clientSet, err := submarinerClientset.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred())
	return clientSet
}
