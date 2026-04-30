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

import (
	"errors"
)

// Options capture the switches available to run the image promoter.
type Options struct {
	// Threads determines how many promotion threads will run
	Threads int

	// Confirm captures a cli flag with the same name. It runs the security
	// scan and promotion when set. If false, the promoter will exit before\
	// making any modifications.
	Confirm bool

	// Use only the latest diff for the manifests. Works only when running in prow.
	UseProwManifestDiff bool

	// Manifest is the path of a manifest file
	Manifest string

	// ThinManifestDir is a directory of thin manifests
	ThinManifestDir string

	// Snapshot takes a registry reference and renders a textual representation of
	// how the imagtes stored there look like to the promoter.
	Snapshot string

	// ManifestBasedSnapshotOf performs a snapshot from the given manifests
	// as opposed of Snapshot which will snapshot a registry across the network
	ManifestBasedSnapshotOf string

	// SeverityThreshold is the level of security vulns to search for.
	SeverityThreshold int

	// OutputFormat is the format we will use for snapshots json/yaml
	OutputFormat string

	// MinimalSnapshot is used in snapshots. but im not sure
	MinimalSnapshot bool

	// SnapshotTag when set, only images with this tag will be snapshotted
	SnapshotTag string

	// ParseOnly is an options that causes the promoter to exit
	// before promoting or generating a snapshot when set to true
	ParseOnly bool

	// When true, sign the container images using the sigstore cosign libraries
	SignImages bool

	// SignerAccount is a service account that will provide the identity
	// when signing promoted images
	SignerAccount string

	// SignCheckReferences list of image references to check for signatures
	SignCheckReferences []string

	// SignCheckFix when true, fix missing signatures
	SignCheckFix bool

	// SignCheckFromDays number of days back to check for signatrures
	SignCheckFromDays int

	// SignCheckToDays complements SignCheckFromDays to enable date ranges
	SignCheckToDays int

	// SignCheckMaxImages limits the number of images to look when verifying
	SignCheckMaxImages int

	// SignCheckIdentity is the account we expect to sign all images
	SignCheckIdentity string

	// SignCheckIssuer is the issuer of the OIDC tokens used to identify the signer
	SignCheckIssuer string

	// SignCheckIdentityRegexp can use a regex to match more than one signer
	SignCheckIdentityRegexp string

	// SignCheckIssuerRegexp can use a regex to match more than one signer OIDC tokens used to identify the signer
	SignCheckIssuerRegexp string

	// MaxSignatureOps maximum number of concurrent signature operations
	MaxSignatureOps int
}

var DefaultOptions = &Options{
	OutputFormat:            "yaml",
	Threads:                 20,
	SeverityThreshold:       -1,
	SignImages:              true,
	SignerAccount:           "krel-trust@k8s-releng-prod.iam.gserviceaccount.com",
	SignCheckFix:            false,
	SignCheckReferences:     []string{},
	SignCheckFromDays:       5,
	SignCheckIdentity:       "krel-trust@k8s-releng-prod.iam.gserviceaccount.com",
	SignCheckIssuer:         "https://accounts.google.com",
	SignCheckIdentityRegexp: "",
	SignCheckIssuerRegexp:   "",
	MaxSignatureOps:         50,
}

func (o *Options) Validate() error {
	// If one of the snapshot options is set, manifests will not be checked
	if o.Snapshot == "" && o.ManifestBasedSnapshotOf == "" {
		if o.Manifest == "" && o.ThinManifestDir == "" {
			return errors.New("at least a manifest file or thin manifest directory have to be specified")
		}
	}

	return nil
}
