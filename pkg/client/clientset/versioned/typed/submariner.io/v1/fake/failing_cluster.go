package fake

import (
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FailingClusters struct {
	submarinerClientsetv1.ClusterInterface

	FailOnCreate error
	FailOnUpdate error
	FailOnDelete error
	FailOnGet    error
	FailOnList   error
}

func (f *FailingClusters) Create(e *v1.Cluster) (*v1.Cluster, error) {
	if f.FailOnCreate != nil {
		return nil, f.FailOnCreate
	}

	return f.ClusterInterface.Create(e)
}

func (f *FailingClusters) Update(e *v1.Cluster) (*v1.Cluster, error) {
	if f.FailOnUpdate != nil {
		return nil, f.FailOnUpdate
	}

	return f.ClusterInterface.Update(e)
}

func (f *FailingClusters) Delete(name string, options *metav1.DeleteOptions) error {
	if f.FailOnDelete != nil {
		return f.FailOnDelete
	}

	return f.ClusterInterface.Delete(name, options)
}

func (f *FailingClusters) Get(name string, options metav1.GetOptions) (*v1.Cluster, error) {
	if f.FailOnGet != nil {
		return nil, f.FailOnGet
	}

	return f.ClusterInterface.Get(name, options)
}

func (f *FailingClusters) List(opts metav1.ListOptions) (*v1.ClusterList, error) {
	if f.FailOnList != nil {
		return nil, f.FailOnList
	}

	return f.ClusterInterface.List(opts)
}
