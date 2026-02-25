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

package registry

import (
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// TagSet is a set of image tags.
type TagSet map[image.Tag]struct{}

// ToTagSet converts a TagSlice to a TagSet.
func (a TagSlice) ToTagSet() TagSet {
	s := make(TagSet, len(a))
	for _, tag := range a {
		s[tag] = struct{}{}
	}
	return s
}

// Minus returns the set difference a - b.
func (a TagSlice) Minus(b TagSlice) TagSet {
	aSet := a.ToTagSet()
	bSet := b.ToTagSet()
	result := make(TagSet)
	for k := range aSet {
		if _, ok := bSet[k]; !ok {
			result[k] = struct{}{}
		}
	}
	return result
}

// Union returns the union of a and b.
func (a TagSlice) Union(b TagSlice) TagSet {
	result := make(TagSet)
	for _, tag := range a {
		result[tag] = struct{}{}
	}
	for _, tag := range b {
		result[tag] = struct{}{}
	}
	return result
}

// Intersection returns elements common to both a and b.
func (a TagSlice) Intersection(b TagSlice) TagSet {
	aSet := a.ToTagSet()
	bSet := b.ToTagSet()
	result := make(TagSet)
	for k := range aSet {
		if _, ok := bSet[k]; ok {
			result[k] = struct{}{}
		}
	}
	return result
}
