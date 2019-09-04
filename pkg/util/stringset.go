package util

type StringSet struct {
	Set map[string]bool
}

func NewStringSet() *StringSet {
	return &StringSet{make(map[string]bool)}
}

func (set *StringSet) Add(s string) bool {
	_, found := set.Set[s]
	set.Set[s] = true
	return !found
}

func (set *StringSet) Contains(s string) bool {
	_, found := set.Set[s]
	return found
}

func (set *StringSet) Size() int {
	return len(set.Set)
}

func (set *StringSet) Delete(s string) {
	delete(set.Set, s)
}
