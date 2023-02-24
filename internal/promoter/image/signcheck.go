/*
Copyright 2023 The Kubernetes Authors.

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
	"fmt"
	"net/url"
	"strings"

	yaml "gopkg.in/yaml.v2"
	"sigs.k8s.io/release-utils/http"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/gcrane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sirupsen/logrus"
	checkresults "sigs.k8s.io/promo-tools/v3/promoter/image/checkresults"
	options "sigs.k8s.io/promo-tools/v3/promoter/image/options"
	"sigs.k8s.io/promo-tools/v3/types/image"
)

var mirrorsList []string

const repositoryPath = "k8s-artifacts-prod/images"

func (di *DefaultPromoterImplementation) GetLatestImages(opts *options.Options) ([]string, error) {
	// If there is a list of images to check in the options
	// we default to checking those.
	if len(opts.SignCheckReferences) > 0 {
		for _, refString := range opts.SignCheckReferences {
			_, err := name.ParseReference(refString)
			if err != nil {
				return nil, fmt.Errorf("invalid image reference %s: %w", refString, err)
			}
		}
		return opts.SignCheckReferences, nil
	}
	return nil, errors.New("automatic image reader not yet implemented")
}

func (di *DefaultPromoterImplementation) getMirrors() ([]string, error) {
	if mirrorsList != nil {
		return mirrorsList, nil
	}
	urls := []string{}
	iurls := map[string]string{}
	manifest, err := http.NewAgent().Get(
		"https://github.com/kubernetes/k8s.io/raw/main/k8s.gcr.io/manifests/k8s-staging-kubernetes/promoter-manifest.yaml",
	)
	if err != nil {
		return nil, fmt.Errorf("downloading promoter manifest: %w", err)
	}

	type entriesList struct {
		Registries []struct {
			Name string `yaml:"name,omitempty"`
			Src  bool   `yaml:"src,omitempty"`
		} `yaml:"registries"`
	}

	entries := entriesList{}
	if err := yaml.Unmarshal(manifest, &entries); err != nil {
		return nil, fmt.Errorf("unmarshalling promoter manifest: %w", err)
	}

	for _, e := range entries.Registries {
		if e.Src {
			continue
		}
		u, err := url.Parse("https://" + e.Name)
		if err != nil {
			return nil, fmt.Errorf("parsing url %s: %w", u, err)
		}
		iurls[u.Hostname()] = u.Hostname()
	}

	for u := range iurls {
		urls = append(urls, u)
	}
	mirrorsList = urls
	return urls, nil
}

func (di *DefaultPromoterImplementation) GetSignatureStatus(
	opts *options.Options, images []string,
) (checkresults.Signature, error) {
	results := checkresults.Signature{}
	mirrors, err := di.getMirrors()
	if err != nil {
		return results, fmt.Errorf("reading mirrors: %w", err)
	}
	logrus.Infof("Checking signatures in %d mirrors", len(mirrors))
	for _, refString := range images {
		ref, err := name.ParseReference(refString)
		if err != nil {
			return results, fmt.Errorf("parsing reference: %w", err)
		}

		digest, err := crane.Digest(refString)
		if err != nil {
			return results, fmt.Errorf("getting digest for %s: %w", refString, err)
		}
		logrus.Infof("image digest: %s", digest)

		targetImages := []string{}
		for _, mirror := range mirrors {
			targetImages = append(targetImages, fmt.Sprintf("%s/%s/%s:%s.sig",
				mirror, repositoryPath, ref.Context().RepositoryStr(),
				strings.ReplaceAll(digest, ":", "-"),
			))
		}
		existing, missing, err := checkObjects(targetImages)
		if err != nil {
			return results, fmt.Errorf("checking objects: %w", err)
		}
		results[refString] = checkresults.CheckList{
			SignatureImage: fmt.Sprintf("%s/%s:%s.sig",
				repositoryPath, ref.Context().RepositoryStr(),
				strings.ReplaceAll(digest, ":", "-"),
			),
			Signed:  existing,
			Missing: missing,
		}
	}
	return results, nil
}

func checkObjects(oList []string) (existing, missing []string, err error) {
	existing = []string{}
	missing = []string{}
	logrus.Infof("Checking %d objects for signatures", len(oList))
	for _, s := range oList {
		e, err := objectExists(s)
		if err != nil {
			return existing, missing, fmt.Errorf("checking reference: %w", err)
		}

		if e {
			existing = append(existing, s)
		} else {
			missing = append(missing, s)
		}
	}
	return existing, missing, nil
}

func objectExists(refString string) (bool, error) {
	// Check
	_, err := crane.Digest(refString)
	if err == nil {
		return true, nil
	}

	if strings.Contains(err.Error(), "MANIFEST_UNKNOWN") {
		return false, nil
	}

	return false, fmt.Errorf("checking if reference exists in the registry: %w", err)
}

// FixMissingSignatures signs an image that has no signatures at all
func (di *DefaultPromoterImplementation) FixMissingSignatures(opts *options.Options, results checkresults.Signature) error {
	for mainImg, res := range results {
		if len(res.Signed) > 0 {
			continue
		}

		logrus.Infof("Signing and replicating %s", mainImg)
		// Build the digest of the first missing one
		digestRef := strings.ReplaceAll(res.Missing[0], ":sha256-", "@sha256:")
		if err := signDigest(opts, digestRef); err != nil {
			return fmt.Errorf("signing %s: %w", digestRef, err)
		}

		for _, targetRef := range res.Missing[1:] {
			if err := replicateReference(opts, res.Missing[0], targetRef); err != nil {
				return fmt.Errorf("replicating signature: %w", err)
			}
		}
	}
	return nil
}

// FixPartialSignatures fixes images that had some signatures but some mirrors
// are missing some signatures
func (di *DefaultPromoterImplementation) FixPartialSignatures(opts *options.Options, results checkresults.Signature) error {
	for mainImg, res := range results {
		if len(res.Missing) == 0 || len(res.Signed) == 0 {
			continue
		}

		logrus.Infof("%s has %d signed copies, %d are missing", mainImg, len(res.Signed), len(res.Missing))

		sourceRef := res.Signed[0]
		for _, targetRef := range res.Missing {
			// Copy the first signature to the target ref
			logrus.Infof("Copying signature from %s to %s", sourceRef, targetRef)

			if err := replicateReference(opts, sourceRef, targetRef); err != nil {
				return fmt.Errorf("replicating signature: %w", err)
			}
		}
	}
	return nil
}

func replicateReference(opts *options.Options, srcRef, dstRef string) error {
	craneOpts := []crane.Option{
		crane.WithAuthFromKeychain(gcrane.Keychain),
		crane.WithUserAgent(image.UserAgent),
	}

	if !opts.SignCheckFix {
		logrus.Infof(" (NOOP) replicating %s to %s ", srcRef, dstRef)
		return nil
	}

	logrus.Infof(" replicating %s to %s ", srcRef, dstRef)

	if err := crane.Copy(srcRef, dstRef, craneOpts...); err != nil {
		return fmt.Errorf(
			"copying signature %s to %s: %w", srcRef, dstRef, err,
		)
	}
	return nil
}

func signDigest(opts *options.Options, refString string) error {
	if !opts.SignCheckFix {
		logrus.Infof(" (NOOP) signing %s", refString)
		return nil
	}
	logrus.Infof(" signing %s", refString)
	// TODO: implement signing
	return nil
}
