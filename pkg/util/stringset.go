package util

import (
	"sync"
)

type StringSet struct {
	syncMutex *sync.Mutex
	set       map[string]bool
}

func NewStringSet() *StringSet {
	return &StringSet{
		syncMutex: &sync.Mutex{},
		set:       make(map[string]bool)}
}

func (set *StringSet) Add(s string) bool {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	_, found := set.set[s]
	set.set[s] = true
	return !found
}

func (set *StringSet) Contains(s string) bool {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	_, found := set.set[s]
	return found
}

func (set *StringSet) Size() int {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	return len(set.set)
}

func (set *StringSet) Delete(s string) bool {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	_, found := set.set[s]
	delete(set.set, s)
	return found
}

func (set *StringSet) Elements() []string {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	elements := make([]string, len(set.set))
	i := 0
	for v := range set.set {
		elements[i] = v
		i++
	}

	return elements
}
