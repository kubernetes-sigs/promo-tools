/*
Copyright 2022 The Kubernetes Authors.

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

package imagepromoter

import options "sigs.k8s.io/promo-tools/v3/promoter/image/options"

func (di *DefaultPromoterImplementation) GetLatestImages(opts *options.Options) ([]string, error) {

}

func (di *DefaultPromoterImplementation) GetSignatureStatus(opts *options.Options, images []string) ([]string, []string, []string, error) {

}

func (di *DefaultPromoterImplementation) FixMissingSignatures(opts *options.Options, images []string) error {

}

func (di *DefaultPromoterImplementation) FixPartialSignatures(opts *options.Options, images []string) error {

}
