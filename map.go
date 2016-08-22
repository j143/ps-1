// Fully persistent data structures. A persistent data structure is a data
// structure that always preserves the previous version of itself when
// it is modified. Such data structures are effectively immutable,
// as their operations do not update the structure in-place, but instead
// always yield a new structure.
//
// Persistent
// data structures typically share structure among themselves.  This allows
// operations to avoid copying the entire data structure.
package ps

import (
	"bytes"
	"fmt"
	"unsafe"
)

// A Map associates unique keys (type string) with values (type Any).
type Map interface {
	// IsNil returns true if the Map is empty
	IsNil() bool

	// Set returns a new map in which key and value are associated.
	// If the key didn't exist before, it's created; otherwise, the
	// associated value is changed.
	// This operation is O(log N) in the number of keys.
	Set(key string, value interface{}) Map

	// UnsafeMutableSet returns the same map in which key and value are associated in-place.
	// If the key didn't exist before, it's created; otherwise, the
	// associated value is changed.
	// This operation is O(log N) in the number of keys.
	// Only use UnsafeMutableSet if you are the only reference-holder of the Map.
	UnsafeMutableSet(key string, value interface{}) Map

	// Delete returns a new map with the association for key, if any, removed.
	// This operation is O(log N) in the number of keys.
	Delete(key string) Map

	// Lookup returns the value associated with a key, if any.  If the key
	// exists, the second return value is true; otherwise, false.
	// This operation is O(log N) in the number of keys.
	Lookup(key string) (interface{}, bool)

	// First returns the "first" value in the map, if any, or nil.
	First() interface{}

	// Size returns the number of key value pairs in the map.
	// This takes O(1) time.
	Size() int

	// ForEach executes a callback on each key value pair in the map.
	ForEach(f func(key string, val interface{}))

	// Keys returns a slice with all keys in this map.
	// This operation is O(N) in the number of keys.
	Keys() []string

	String() string
}

// Immutable (i.e. persistent) associative array
const childCount = 8
const shiftSize = 3

type tree struct {
	count    int
	hash     uint64 // hash of the key (used for tree balancing)
	key      string
	value    interface{}
	children [childCount]*tree
}

var nilMap = &tree{}

// Recursively set nilMap's subtrees to point at itself.
// This eliminates all nil pointers in the map structure.
// All map nodes are created by cloning this structure so
// they avoid the problem too.
func init() {
	for i := range nilMap.children {
		nilMap.children[i] = nilMap
	}
}

// NewMap allocates a new, persistent map from strings to values of
// any type.
// This is currently implemented as a path-copying binary tree.
func NewMap() Map {
	return nilMap
}

func (self *tree) IsNil() bool {
	return self == nilMap
}

// clone returns an exact duplicate of a tree node
func (self *tree) clone() *tree {
	var m tree
	m = *self
	return &m
}

// constants for FNV-1a hash algorithm
const (
	offset64 uint64 = 14695981039346656037
	prime64  uint64 = 1099511628211
)

type unsafeString struct {
	Data uintptr
	Len  int
}

type unsafeSlice struct {
	Data uintptr
	Len  int
	Cap  int
}

var zeroByteSlice = []byte{}

// bytesView returns a view of the string as a []byte.
// It doesn't incur allocation and copying caused by conversion but it's
// unsafe, use with care.
func bytesView(v string) []byte {
	if len(v) == 0 {
		return zeroByteSlice
	}

	sx := (*unsafeString)(unsafe.Pointer(&v))
	bx := unsafeSlice{sx.Data, sx.Len, sx.Len}
	return *(*[]byte)(unsafe.Pointer(&bx))
}

// hashKey returns a hash code for a given string
func hashKey(key string) uint64 {
	hash := offset64

	for _, b := range bytesView(key) {
		hash ^= uint64(b)
		hash *= prime64
	}
	return hash
}

// Set returns a new map similar to this one but with key and value
// associated.  If the key didn't exist, it's created; otherwise, the
// associated value is changed.
func (self *tree) Set(key string, value interface{}) Map {
	hash := hashKey(key)
	return setLowLevel(self, hash, hash, key, value)
}

func setLowLevel(self *tree, partialHash, hash uint64, key string, value interface{}) *tree {
	if self.IsNil() { // an empty tree is easy
		m := self.clone()
		m.count = 1
		m.hash = hash
		m.key = key
		m.value = value
		return m
	}

	if hash != self.hash {
		m := self.clone()
		i := partialHash % childCount
		m.children[i] = setLowLevel(self.children[i], partialHash>>shiftSize, hash, key, value)
		// update count if we added a new object
		if m.children[i].count > self.children[i].count {
			m.count++
		}
		return m
	}

	// did we find a hash collision?
	if key != self.key {
		oops := fmt.Sprintf("Hash collision between: '%s' and '%s'.  Please report to https://github.com/mndrix/ps/issues/new", self.key, key)
		panic(oops)
	}

	// replacing a key's previous value
	m := self.clone()
	m.value = value
	return m
}

// UnsafeMutableSet is the in-place mutable version of Set. Only use if
// you are the only reference-holder of the Map.
func (self *tree) UnsafeMutableSet(key string, value interface{}) Map {
	hash := hashKey(key)
	return mutableSetLowLevel(self, hash, hash, key, value)
}

func mutableSetLowLevel(self *tree, partialHash, hash uint64, key string, value interface{}) *tree {
	if self.IsNil() { // an empty tree is easy
		m := self.clone()
		m.count = 1
		m.hash = hash
		m.key = key
		m.value = value
		return m
	}

	if hash != self.hash {
		i := partialHash % childCount
		oldChildCount := self.children[i].count
		self.children[i] = mutableSetLowLevel(self.children[i], partialHash>>shiftSize, hash, key, value)
		// update count if we added a new object
		if oldChildCount < self.children[i].count {
			self.count++
		}
		return self
	}

	// did we find a hash collision?
	if key != self.key {
		oops := fmt.Sprintf("Hash collision between: '%s' and '%s'.  Please report to https://github.com/mndrix/ps/issues/new", self.key, key)
		panic(oops)
	}

	// replacing a key's previous value
	self.value = value
	return self
}

// modifies a map by recalculating its key count based on the counts
// of its subtrees
func recalculateCount(m *tree) {
	count := 0
	for _, t := range m.children {
		count += t.Size()
	}
	m.count = count + 1 // add one to count ourself
}

func (m *tree) Delete(key string) Map {
	hash := hashKey(key)
	newMap, _ := deleteLowLevel(m, hash, hash)
	return newMap
}

func deleteLowLevel(self *tree, partialHash, hash uint64) (*tree, bool) {
	// empty trees are easy
	if self.IsNil() {
		return self, false
	}

	if hash != self.hash {
		i := partialHash % childCount
		child, found := deleteLowLevel(self.children[i], partialHash>>shiftSize, hash)
		if !found {
			return self, false
		}
		newMap := self.clone()
		newMap.children[i] = child
		recalculateCount(newMap)
		return newMap, true // ? this wasn't in the original code
	}

	// we must delete our own node
	if self.isLeaf() { // we have no children
		return nilMap, true
	}
	/*
	   if self.subtreeCount() == 1 { // only one subtree
	       for _, t := range self.children {
	           if t != nilMap {
	               return t, true
	           }
	       }
	       panic("Tree with 1 subtree actually had no subtrees")
	   }
	*/

	// find a node to replace us
	i := -1
	size := -1
	for j, t := range self.children {
		if t.Size() > size {
			i = j
			size = t.Size()
		}
	}

	// make chosen leaf smaller
	replacement, child := self.children[i].deleteLeftmost()
	newMap := replacement.clone()
	for j := range self.children {
		if j == i {
			newMap.children[j] = child
		} else {
			newMap.children[j] = self.children[j]
		}
	}
	recalculateCount(newMap)
	return newMap, true
}

// delete the leftmost node in a tree returning the node that
// was deleted and the tree left over after its deletion
func (m *tree) deleteLeftmost() (*tree, *tree) {
	if m.isLeaf() {
		return m, nilMap
	}

	for i, t := range m.children {
		if t != nilMap {
			deleted, child := t.deleteLeftmost()
			newMap := m.clone()
			newMap.children[i] = child
			recalculateCount(newMap)
			return deleted, newMap
		}
	}
	panic("Tree isn't a leaf but also had no children. How does that happen?")
}

// isLeaf returns true if this is a leaf node
func (m *tree) isLeaf() bool {
	return m.Size() == 1
}

// returns the number of child subtrees we have
func (m *tree) subtreeCount() int {
	count := 0
	for _, t := range m.children {
		if t != nilMap {
			count++
		}
	}
	return count
}

func (m *tree) Lookup(key string) (interface{}, bool) {
	hash := hashKey(key)
	return lookupLowLevel(m, hash, hash)
}

func lookupLowLevel(self *tree, partialHash, hash uint64) (interface{}, bool) {
	if self.IsNil() { // an empty tree is easy
		return nil, false
	}

	if hash != self.hash {
		i := partialHash % childCount
		return lookupLowLevel(self.children[i], partialHash>>shiftSize, hash)
	}

	// we found it
	return self.value, true
}

func (m *tree) First() interface{} {
	return m.value
}

func (m *tree) Size() int {
	return m.count
}

func (m *tree) ForEach(f func(key string, val interface{})) {
	if m.IsNil() {
		return
	}

	// ourself
	f(m.key, m.value)

	// children
	for _, t := range m.children {
		if t != nilMap {
			t.ForEach(f)
		}
	}
}

func (m *tree) Keys() []string {
	keys := make([]string, m.Size())
	i := 0
	m.ForEach(func(k string, v interface{}) {
		keys[i] = k
		i++
	})
	return keys
}

// make it easier to display maps for debugging
func (m *tree) String() string {
	keys := m.Keys()
	buf := bytes.NewBufferString("{")
	for _, key := range keys {
		val, _ := m.Lookup(key)
		fmt.Fprintf(buf, "%s: %s, ", key, val)
	}
	fmt.Fprintf(buf, "}\n")
	return buf.String()
}
