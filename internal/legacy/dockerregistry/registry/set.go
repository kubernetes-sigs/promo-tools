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
	"sigs.k8s.io/promo-tools/v4/internal/legacy/container"
	"sigs.k8s.io/promo-tools/v4/types/image"
)

// Various set manipulation operations. Some set operations are missing,
// because, we don't use them.

// ToTagSet converts a TagSlice to a TagSet.
func (a TagSlice) ToTagSet() container.Set[image.Tag] {
	return container.NewSet(a...)
}

// Minus is a set operation.
func (a TagSlice) Minus(b TagSlice) container.Set[image.Tag] {
	aSet := a.ToTagSet()
	bSet := b.ToTagSet()
	cSet := aSet.Minus(bSet)

	return cSet
}

// Union is a set operation.
func (a TagSlice) Union(b TagSlice) container.Set[image.Tag] {
	aSet := a.ToTagSet()
	bSet := b.ToTagSet()
	cSet := aSet.Union(bSet)

	return cSet
}

// Intersection is a set operation.
func (a TagSlice) Intersection(b TagSlice) container.Set[image.Tag] {
	aSet := a.ToTagSet()
	bSet := b.ToTagSet()
	cSet := aSet.Intersection(bSet)

	return cSet
}
