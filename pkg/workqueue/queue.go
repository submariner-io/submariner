package workqueue

import (
	"fmt"
	"time"

	"github.com/submariner-io/submariner/pkg/log"
	"golang.org/x/time/rate"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

type ProcessFunc func(name, namespace string) (bool, error)

type Interface interface {
	Enqueue(obj interface{})

	Run(stopCh <-chan struct{}, process ProcessFunc)

	ShutDown()
}

type queueType struct {
	workqueue.RateLimitingInterface

	name string
}

func New(name string) Interface {
	return &queueType{
		RateLimitingInterface: workqueue.NewNamedRateLimitingQueue(workqueue.NewMaxOfRateLimiter(
			// exponential per-item rate limiter
			workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 30*time.Second),
			// overall rate limiter (not per item)
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
		), name),
		name: name,
	}
}

func (q *queueType) Enqueue(obj interface{}) {
	var key string
	var err error
	if key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}

	klog.V(log.TRACE).Infof("%s: enqueueing key %v", q.name, key)
	q.AddRateLimited(key)
}

func (q *queueType) Run(stopCh <-chan struct{}, process ProcessFunc) {
	go wait.Until(func() {
		for q.processNextWorkItem(process) {
		}
	}, time.Second, stopCh)
}

func (q *queueType) processNextWorkItem(process ProcessFunc) bool {
	key, shutdown := q.Get()
	if shutdown {
		return false
	}

	defer q.Done(key)

	requeue, err := func() (bool, error) {
		ns, name, err := cache.SplitMetaNamespaceKey(key.(string))
		if err != nil {
			return false, err
		}

		return process(name, ns)
	}()

	if err != nil {
		utilruntime.HandleError(fmt.Errorf("%s: Failed to process object with key %q: %v", q.name, key, err))
	}

	if requeue {
		q.AddRateLimited(key)
	} else {
		q.Forget(key)
	}

	return true
}
