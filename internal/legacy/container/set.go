/*
Copyright 2019 The Kubernetes Authors.

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

package container

// Set is a basic set-like data structure.
type Set[K comparable] map[K]struct{}

// NewSet contructs a Set with the specified items.
func NewSet[K comparable](items ...K) Set[K] {
	s := make(Set[K])
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}

// Insert inserts an item into the set.
func (s Set[K]) Insert(item K) {
	s[item] = struct{}{}
}

// Minus returns a new set, by subtracting everything in b from a.
func (a Set[K]) Minus(b Set[K]) Set[K] {
	c := make(Set[K])
	for k, v := range a {
		c[k] = v
	}
	for k := range b {
		delete(c, k)
	}
	return c
}

// Union takes two sets and returns their union in a new set.
func (a Set[K]) Union(b Set[K]) Set[K] {
	c := make(Set[K])
	for k, v := range a {
		c[k] = v
	}
	for k, v := range b {
		c[k] = v
	}
	return c
}

// Intersection takes two sets and returns elements common to both. Note that we
// throw away information about the values of the elements in b.
func (a Set[K]) Intersection(b Set[K]) Set[K] {
	c := make(Set[K])
	for k, v := range a {
		if _, ok := b[k]; ok {
			c[k] = v
		}
	}
	return c
}
