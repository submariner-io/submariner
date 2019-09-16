package util

import (
	"sync"
)

type StringSet struct {
	syncMutex *sync.Mutex
	Set       map[string]bool
}

func NewStringSet() *StringSet {
	return &StringSet{
		syncMutex: &sync.Mutex{},
		Set:       make(map[string]bool)}
}

func (set *StringSet) Add(s string) bool {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	_, found := set.Set[s]
	set.Set[s] = true
	return !found
}

func (set *StringSet) Contains(s string) bool {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	_, found := set.Set[s]
	return found
}

func (set *StringSet) Size() int {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	return len(set.Set)
}

func (set *StringSet) Delete(s string) {
	set.syncMutex.Lock()
	defer set.syncMutex.Unlock()

	delete(set.Set, s)
}
