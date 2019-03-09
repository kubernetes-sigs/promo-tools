/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inventory

import (
	"fmt"
	"io/ioutil"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"

	yaml "gopkg.in/yaml.v2"

	json "github.com/kubernetes-sigs/k8s-container-image-promoter/lib/json"
	"github.com/kubernetes-sigs/k8s-container-image-promoter/lib/stream"
)

// MakeSyncContext creates a SyncContext.
func MakeSyncContext(
	mi MasterInventory,
	verbosity, threads int,
	deleteExtraTags, dryRun bool) SyncContext {

	return SyncContext{
		Verbosity:       verbosity,
		Threads:         threads,
		DeleteExtraTags: deleteExtraTags,
		DryRun:          dryRun,
		Inv:             mi}
}

// Basic logging.

// Infof logs at INFO level.
func (sc *SyncContext) Infof(s string, v ...interface{}) {
	if sc.Verbosity > 2 {
		fmt.Printf(s, v...)
	}
}

// Warnf logs at WARN level.
func (sc *SyncContext) Warnf(s string, v ...interface{}) {
	if sc.Verbosity > 1 {
		fmt.Printf(s, v...)
	}
}

// Errorf logs at ERROR level.
func (sc *SyncContext) Errorf(s string, v ...interface{}) {
	if sc.Verbosity > 0 {
		fmt.Printf(s, v...)
	}
}

// Fatalf logs at FATAL level.
func (sc *SyncContext) Fatalf(s string, v ...interface{}) {
	fmt.Printf(s, v...)
}

// Info logs at INFO level.
func (sc *SyncContext) Info(v ...interface{}) {
	if sc.Verbosity > 2 {
		fmt.Println(v...)
	}
}

// Warn logs at WARN level.
func (sc *SyncContext) Warn(v ...interface{}) {
	if sc.Verbosity > 1 {
		fmt.Println(v...)
	}
}

// Error logs at ERROR level.
func (sc *SyncContext) Error(v ...interface{}) {
	if sc.Verbosity > 0 {
		fmt.Println(v...)
	}
}

// Fatal logs at FATAL level.
func (sc *SyncContext) Fatal(v ...interface{}) {
	fmt.Println(v...)
}

// ParseManifestFromFile parses a Manifest from a filepath.
func ParseManifestFromFile(filePath string) Manifest {
	bytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		fmt.Printf("Failed to read the manifest at '%v'\n", filePath)
		log.Fatal(err)
	}
	mfest, err := ParseManifest(bytes)
	if err != nil {
		fmt.Printf("Failed to parse the manifest at '%v'\n", filePath)
		log.Fatal(err)
	}

	return mfest
}

// ParseManifest parses a Manifest from a byteslice. This function is separate
// from ParseManifestFromFile() so that it can be tested independently.
func ParseManifest(bytes []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.UnmarshalStrict(bytes, &m); err != nil {
		return m, err
	}

	return m, m.Validate()
}

// Validate checks for semantic errors in the yaml fields (the structure of the
// yaml is checked during unmarshaling).
func (m Manifest) Validate() error {
	return validateImages(m.Images)
}

func validateImages(images []Image) error {
	for _, image := range images {
		for digest, tagSlice := range image.Dmap {
			if err := validateDigest(digest); err != nil {
				return err
			}

			for _, tag := range tagSlice {
				if err := validateTag(tag); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateDigest(digest Digest) error {
	validDigest := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	if !validDigest.Match([]byte(digest)) {
		return fmt.Errorf("invalid digest: %v", digest)
	}
	return nil
}

func validateTag(tag Tag) error {
	var validTag = regexp.MustCompile(`^[\w][\w.-]{0,127}$`)
	if !validTag.Match([]byte(tag)) {
		return fmt.Errorf("invalid tag: %v", tag)
	}
	return nil
}

// PrettyValue creates a prettified string representation of MasterInventory.
func (mi *MasterInventory) PrettyValue() string {
	var b strings.Builder
	for regName, v := range *mi {
		fmt.Fprintln(&b, regName)
		imageNamesSorted := make([]string, 0)
		for imageName := range v {
			imageNamesSorted = append(imageNamesSorted, string(imageName))
		}
		sort.Strings(imageNamesSorted)
		for _, imageName := range imageNamesSorted {
			fmt.Fprintf(&b, "  %v\n", imageName)
			digestTags, ok := v[ImageName(imageName)]
			if !ok {
				continue
			}
			digestSorted := make([]string, 0)
			for digest := range digestTags {
				digestSorted = append(digestSorted, string(digest))
			}
			sort.Strings(digestSorted)
			for _, digest := range digestSorted {
				fmt.Fprintf(&b, "    %v\n", digest)
				tags, ok := digestTags[Digest(digest)]
				if !ok {
					continue
				}
				for _, tag := range tags {
					fmt.Fprintf(&b, "      %v\n", tag)
				}
			}
		}
	}
	return b.String()
}

// PrettyValue converts a Registry to a prettified string representation.
func (r *Registry) PrettyValue() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%v (%v)\n", r.RegistryNameLong, r.RegistryName)
	fmt.Fprintln(&b, r.RegInvImageDigest.PrettyValue())
	return b.String()
}

// PrettyValue converts a RegInvImageDigest to a prettified string
// representation.
func (riid *RegInvImageDigest) PrettyValue() string {
	var b strings.Builder
	ids := make([]ImageDigest, 0)
	for id := range *riid {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		iID := string(ids[i].ImageName) + string(ids[i].Digest)
		jID := string(ids[j].ImageName) + string(ids[i].Digest)
		return iID < jID
	})
	for _, id := range ids {
		fmt.Fprintf(&b, "  %v\n", id.ImageName)
		fmt.Fprintf(&b, "    %v\n", id.Digest)
		tagArray, ok := (*riid)[id]
		if !ok {
			continue
		}
		tagArraySorted := make([]string, 0)
		for _, tag := range tagArray {
			tagArraySorted = append(tagArraySorted, string(tag))
		}
		sort.Strings(tagArraySorted)
		for _, tag := range tagArraySorted {
			fmt.Fprintf(&b, "      %v\n", tag)
		}
	}
	return b.String()
}

func getJSONSFromProcess(req stream.ExternalRequest) (json.Objects, Errors) {
	var jsons json.Objects
	errors := make(Errors, 0)
	stdoutReader, stderrReader, err := req.StreamProducer.Produce()
	if err != nil {
		errors = append(errors, Error{
			Context: "running process",
			Error:   err})
	}
	jsons, err = json.Consume(stdoutReader)
	if err != nil {
		errors = append(errors, Error{
			Context: "parsing JSON",
			Error:   err})
	}
	be, err := ioutil.ReadAll(stderrReader)
	if err != nil {
		errors = append(errors, Error{
			Context: "reading process stderr",
			Error:   err})
	}
	if len(be) > 0 {
		errors = append(errors, Error{
			Context: "process had stderr",
			Error:   fmt.Errorf("%v", string(be))})
	}
	err = req.StreamProducer.Close()
	if err != nil {
		errors = append(errors, Error{
			Context: "closing process",
			Error:   err})
	}
	return jsons, errors
}

// ReadImageNames only works for streams that interpret json.
func (sc *SyncContext) ReadImageNames(
	mkProducer func(RegistryName) stream.Producer) {
	// Collect all images in sc.Inv (the src and dest reqgistry names found in
	// the manifest).
	var populateRequests PopulateRequests = func(
		sc *SyncContext, reqs chan<- stream.ExternalRequest) {

		for registryName := range sc.Inv {
			var req stream.ExternalRequest
			req.RequestParams = registryName
			req.StreamProducer = mkProducer(registryName)
			reqs <- req
		}
	}
	var processRequest ProcessRequest = func(
		sc *SyncContext,
		reqs <-chan stream.ExternalRequest,
		requestResults chan<- RequestResult,
		wg *sync.WaitGroup,
		mutex *sync.Mutex) {

		defer wg.Done()
		for req := range reqs {
			reqRes := RequestResult{Context: req}
			jsons, errors := getJSONSFromProcess(req)
			if len(errors) > 0 {
				// Skip this request if it has errors.
				reqRes.Errors = errors
				requestResults <- reqRes
				continue
			}
			extendMe := make(RegInvImage)
			for _, json := range jsons {
				imageName, err := extractImageName(
					json,
					req.RequestParams.(RegistryName))

				if err != nil {
					// Record any errors while parsing each JSON.
					errors = append(errors, Error{
						Context: fmt.Sprintf(
							"extractImageName (skipping): %v",
							json),
						Error: err})
					continue
				}
				extendMe[imageName] = nil
			}
			reqRes.Errors = errors
			requestResults <- reqRes
			mutex.Lock()
			sc.Inv[req.RequestParams.(RegistryName)] = extendMe
			mutex.Unlock()
		}
	}
	sc.ExecRequests(populateRequests, processRequest)
}

// ReadDigestsAndTags runs `gcloud container images list-tags
// gcr.io/louhi-gke-k8s/etcd --format=json` For each image name, retrieve all
// digests and corresponding tags (if any).
func (sc *SyncContext) ReadDigestsAndTags(
	mkProducer func(RegistryName, ImageName) stream.Producer) {

	var populateRequests PopulateRequests = func(
		sc *SyncContext,
		reqs chan<- stream.ExternalRequest) {

		for registryName, imagesMap := range sc.Inv {
			for imgName := range imagesMap {
				var req stream.ExternalRequest
				req.StreamProducer = mkProducer(registryName, imgName)
				req.RequestParams = DigestTagsContext{
					ImageName:    imgName,
					RegistryName: registryName}
				reqs <- req
			}
		}
	}
	var processRequest ProcessRequest = func(
		sc *SyncContext,
		reqs <-chan stream.ExternalRequest,
		requestResults chan<- RequestResult,
		wg *sync.WaitGroup,
		mutex *sync.Mutex) {

		defer wg.Done()
		for req := range reqs {
			reqRes := RequestResult{Context: req}
			jsons, errors := getJSONSFromProcess(req)
			if len(errors) > 0 {
				// Skip this request if it has errors.
				reqRes.Errors = errors
				requestResults <- reqRes
				continue
			}
			extendMe := make(DigestTags)
			for _, json := range jsons {
				digestTags, err := extractDigestTags(json)
				if err != nil {
					// Record any errors while parsing each JSON.
					errors = append(errors, Error{
						Context: fmt.Sprintf(
							"extractDigestTags (skipping): %v", json),
						Error: err})
					continue
				}
				extendMe.Overwrite(digestTags)
				t := req.RequestParams.(DigestTagsContext)
				mutex.Lock()
				sc.Inv[t.RegistryName][t.ImageName] = extendMe
				mutex.Unlock()
			}
			reqRes.Errors = errors
			requestResults <- reqRes
		}
	}
	sc.ExecRequests(populateRequests, processRequest)
}

// Do some sanitizing. For instance, remove the unnecessary
// "timpstamp" information that GCR gives us.
func extractDigestTags(json json.Object) (DigestTags, error) {
	var digest Digest
	var tags TagSlice
	for k, v := range json {
		switch k {
		case "timestamp":
			continue
		case "digest":
			digestStr := v.(string)
			digest = Digest(digestStr)
		case "tags":
			// Need to json-decode this inner interface{} value.
			tagsInter := v.([]interface{})
			for _, tagInter := range tagsInter {
				tagStr := tagInter.(string)
				tags = append(tags, Tag(tagStr))
			}
		default:
			// Skip unknown tags.
			continue
		}
	}
	if digest == "" {
		return nil, fmt.Errorf("could not extract DigestTags: %v", json)
	}
	return DigestTags{digest: tags}, nil
}

// ExecRequests uses the Worker Pool pattern, where MaxConcurrentRequests
// determines the number of workers to spawn.
func (sc *SyncContext) ExecRequests(
	populateRequests PopulateRequests,
	processRequest ProcessRequest) {
	// Run requests.
	MaxConcurrentRequests := 10
	if sc.Threads > 0 {
		MaxConcurrentRequests = sc.Threads
	}
	mutex := &sync.Mutex{}
	reqs := make(chan stream.ExternalRequest, MaxConcurrentRequests)
	requestResults := make(chan RequestResult)
	// We have to use a WaitGroup, because even though we know beforehand the
	// number of workers, we don't know the number of jobs.
	wg := new(sync.WaitGroup)
	// Log any errors encountered.
	go func() {
		for reqRes := range requestResults {
			if len(reqRes.Errors) > 0 {
				sc.Errorf(
					"Request %v: error(s) encountered: %v\n",
					reqRes.Context,
					reqRes.Errors)
			} else {
				sc.Infof("Request %v: OK\n", reqRes.Context.RequestParams)
			}
		}
	}()
	wg.Add(MaxConcurrentRequests)
	for w := 0; w < MaxConcurrentRequests; w++ {
		go processRequest(sc, reqs, requestResults, wg, mutex)
	}
	populateRequests(sc, reqs)
	close(reqs)
	// Wait for all workers to finish draining the jobs.
	wg.Wait()
	// Close requestResults channel because no more new jobs are being created
	// (it's OK to close a channel even if it has nonzero length). On the other
	// hand, we cannot close the channel before we Wait() for the workers to
	// finish, because we would end up closing it too soon, and a worker would
	// end up trying to send a result to the already-closed channel.
	//
	// NOTE: This requestResults channel is only useful if we want a central
	// place to process how each request happened (good for maybe debugging slow
	// reqs? benchmarking?). If we just want to print something, for example,
	// whenever there's an error, we could do away with this channel and just
	// spit out to STDOUT wheneven we encounter an error, from whichever
	// goroutine (no need to put the error into a channel for consumption from a
	// single point).
	close(requestResults)
}

// extractImageName gets the image name from a raw JSON object. The interesting
// thing it does is it strips the leading registry name if it exists.
func extractImageName(
	json json.Object,
	registryName RegistryName) (ImageName, error) {

	var imageName ImageName
	for k, v := range json {
		switch k {
		case "name":
			imageNameStr := v.(string)
			registryPrefix := string(registryName) + "/"
			imageNameOnly := strings.TrimPrefix(imageNameStr, registryPrefix)
			imageName = ImageName(imageNameOnly)
		default:
			// Skip unknown tags.
			continue
		}
	}
	if imageName != "" {
		return imageName, nil
	}
	return "", fmt.Errorf("could not extract image name: %v", json)
}

// Overwrite insert's b's values into a.
func (a DigestTags) Overwrite(b DigestTags) {
	for k, v := range b {
		a[k] = v
	}
}

// ToFQIN combines a RegistryName, ImageName, and Digest to form a
// fully-qualified image name (FQIN).
func ToFQIN(registryName RegistryName,
	imageName ImageName,
	digest Digest) string {

	return string(registryName) + "/" + string(imageName) + "@" + string(digest)
}

// ToPQIN converts a RegistryName, ImageName, and Tag to form a
// partially-qualified image name (PQIN). It's less exact than a FQIN because
// the digest information is not used.
func ToPQIN(registryName RegistryName, imageName ImageName, tag Tag) string {
	return string(registryName) + "/" + string(imageName) + ":" + string(tag)
}

// ShowLostImages logs all images in Manifest which are missing from src.
func (sc *SyncContext) ShowLostImages(mfest Manifest) {
	src := sc.Inv[mfest.Registries.Src].ToRegInvImageDigest()

	// lost = all images that cannot be found from src.
	lost := mfest.ToRegInvImageDigest().Minus(src)
	if len(lost) > 0 {
		sc.Errorf(
			"Lost images (all images in Manifest that cannot be found"+
				" from src registry %v):\n",
			mfest.Registries.Src)
		for imageDigest := range lost {
			fqin := ToFQIN(
				mfest.Registries.Src,
				imageDigest.ImageName,
				imageDigest.Digest)
			sc.Errorf(
				"image %v in manifest is NOT in src registry!\n",
				fqin)
		}
	} else {
		sc.Infof(
			"Lost images (all images in Manifest that cannot be found from"+
				" src registry %v):\n  <none>\n",
			mfest.Registries.Src)
	}
}

func (sc *SyncContext) mkPopReq(
	mfest Manifest, // seems odd
	thing RegInvImageTag,
	tp TagOp,
	oldDigest Digest,
	mkProducer func( // seems odd
		RegistryName,
		RegistryName,
		ImageName,
		Digest,
		Tag,
		TagOp) stream.Producer,
	reqs chan<- stream.ExternalRequest) {

	dest := sc.Inv[mfest.Registries.Dest]
	for imageTag, digest := range thing {
		var req stream.ExternalRequest
		req.StreamProducer = mkProducer(
			mfest.Registries.Src,
			mfest.Registries.Dest,
			imageTag.ImageName,
			digest,
			imageTag.Tag,
			tp)
		tpStr := ""
		fqin := ToFQIN(
			mfest.Registries.Dest,
			imageTag.ImageName,
			digest)
		movePointerOnly := false
		tpStr = tp.PrettyValue()
		if tp == Move {
			// It could be that we are either moving a tag by
			// uploading a new digest to dest, or that we are just
			// moving a tag from an already-uploaded digest in dest.
			// (The latter should be a lot quicker because there is no
			// need to copy the digest into the registry as it already
			// exists there). We should make a distinction and say so in
			// the logs.
			imageDigest := ImageDigest{
				ImageName: imageTag.ImageName,
				Digest:    digest,
			}
			_, movePointerOnly = dest.ToRegInvImageDigest()[imageDigest]
		}
		// Display msg when promoting.
		msg := fmt.Sprintf(
			"%s tag: %s -> %s\n", tpStr, imageTag.Tag, fqin)
		// For moving tags, display a more verbose message (show the
		// what the tag currently points to).
		if tp == Move {
			msg = fmt.Sprintf(`%s tag: %s
  OLD -> %s/%s@%s
  NEW -> %s/%s@%s
`,
				tpStr,
				imageTag.Tag,
				mfest.Registries.Dest,
				imageTag.ImageName,
				oldDigest,
				mfest.Registries.Dest,
				imageTag.ImageName, digest)
			if movePointerOnly {
				msg += fmt.Sprintf(
					"    (Digest %v already exists in destination.)",
					digest)
			} else {
				msg += fmt.Sprintf(
					"    (Digest %v does not yet exist"+
						" in destination.)",
					digest)
			}
		}
		sc.Info(msg)

		// Save some information about this request. It's a bit like
		// HTTP "headers".
		req.RequestParams = PromotionRequest{
			tp,
			mfest.Registries,
			imageTag.ImageName,
			digest,
			oldDigest,
			imageTag.Tag,
		}
		reqs <- req
	}

}

// mkPopulateRequestsForPromotion creates all requests necessary to reconcile
// the Manifest against the state of the world.
func mkPopulateRequestsForPromotion(
	mfest Manifest,
	promotionCandidatesIT RegInvImageTag,
	mkProducer func(
		RegistryName,
		RegistryName,
		ImageName,
		Digest,
		Tag,
		TagOp) stream.Producer,
) PopulateRequests {
	return func(sc *SyncContext, reqs chan<- stream.ExternalRequest) {
		destIT := sc.Inv[mfest.Registries.Dest].ToRegInvImageTag()
		// For all promotionCandidates that are not already in the destination,
		// their promotion type is "Add".
		sc.mkPopReq(
			mfest,
			promotionCandidatesIT.Minus(destIT),
			Add,
			"",
			mkProducer,
			reqs)
		// Audit the intersection (make sure already-existing tags are pointing
		// to the digest specified in the manifest).
		toAudit := promotionCandidatesIT.Intersection(destIT)
		for imageTag, digest := range toAudit {
			if liveDigest, ok := destIT[imageTag]; ok {
				// dest has this imageTag already; need to audit.
				pqin := ToPQIN(
					mfest.Registries.Dest,
					imageTag.ImageName,
					imageTag.Tag)
				if digest == liveDigest {
					// NOP if dest's imageTag is already pointing to the same
					// digest as in the manifest.

					sc.Infof(
						"skipping: image '%v' already points to the same"+
							" digest (%v) as in the Manifest\n",
						pqin,
						digest)
					continue
				} else {
					// Dest's tag is pointing to the wrong digest! Need to move
					// the tag.
					sc.Warnf(
						"Warning: image '%v' is pointing to the wrong digest"+
							"\n       got: %v\n  expected: %v\n",
						pqin,
						liveDigest,
						digest)
					sc.mkPopReq(
						mfest,
						RegInvImageTag{imageTag: digest},
						Move,
						liveDigest,
						mkProducer,
						reqs)
				}
			}
		}
		mfestIT := mfest.ToRegInvImageTag()
		if sc.DeleteExtraTags {
			sc.mkPopReq(
				mfest,
				destIT.Minus(mfestIT),
				Delete,
				"",
				mkProducer,
				reqs)
		} else {
			// Warn the user about extra tags:
			xtras := make([]string, 0)
			for imageTag := range destIT.Minus(promotionCandidatesIT) {
				xtras = append(xtras, fmt.Sprintf(
					"%s:%s",
					imageTag.ImageName,
					imageTag.Tag))
			}
			sort.Strings(xtras)
			for _, img := range xtras {
				sc.Warnf("Warning: extra tag found in dest: %s\n", img)
			}
		}
	}
}

// GetPromotionCandidatesIT returns those images that are due for promotion.
func (sc *SyncContext) GetPromotionCandidatesIT(
	mfest Manifest) RegInvImageTag {

	src := sc.Inv[mfest.Registries.Src]

	// promotionCandidates = all images in the manifest that can be found from
	// src. But, this is filtered later to remove those ones that are already in
	// dest (see mkPopulateRequestsForPromotion).
	promotionCandidates := mfest.ToRegInvImageDigest().Intersection(
		src.ToRegInvImageDigest())
	sc.Infof(
		"To promote (intersection of Manifest and src registry %v):\n%v",
		mfest.Registries.Src,
		promotionCandidates.PrettyValue())

	if len(promotionCandidates) > 0 {
		sc.Infof(
			"To promote (after removing already-promoted images):\n%v",
			promotionCandidates.PrettyValue())
		sc.Info("---------- BEGIN PROMOTION ----------")
	} else {
		sc.Infof(
			"To promote (after removing already-promoted images):\n  <none>\n")
	}

	promotionCandidatesIT := promotionCandidates.ToRegInvImageTag()

	if len(promotionCandidatesIT) == 0 {
		fmt.Println("Nothing to promote.")
	}

	return promotionCandidatesIT
}

// Promote perferms container image promotion by realizing the intent in the
// Manifest.
func (sc *SyncContext) Promote(
	mfest Manifest,
	mkProducer func(
		RegistryName,
		RegistryName,
		ImageName,
		Digest,
		Tag,
		TagOp) stream.Producer,
	customProcessRequest *ProcessRequest) {

	mfestID := (mfest.ToRegInvImageDigest())

	sc.Infof("Desired state:\n%v", mfestID.PrettyValue())
	sc.ShowLostImages(mfest)

	promotionCandidatesIT := sc.GetPromotionCandidatesIT(mfest)

	var populateRequests = mkPopulateRequestsForPromotion(
		mfest,
		promotionCandidatesIT,
		mkProducer)

	var processRequest ProcessRequest
	var processRequestReal ProcessRequest = func(
		sc *SyncContext,
		reqs <-chan stream.ExternalRequest,
		requestResults chan<- RequestResult,
		wg *sync.WaitGroup,
		mutex *sync.Mutex) {

		defer wg.Done()
		for req := range reqs {
			reqRes := RequestResult{Context: req}
			errors := make(Errors, 0)
			stdoutReader, stderrReader, err := req.StreamProducer.Produce()
			if err != nil {
				errors = append(errors, Error{
					Context: "running process",
					Error:   err})
			}
			b, err := ioutil.ReadAll(stdoutReader)
			if err != nil {
				errors = append(errors, Error{
					Context: "reading process stdout",
					Error:   err})
			}
			be, err := ioutil.ReadAll(stderrReader)
			if err != nil {
				errors = append(errors, Error{
					Context: "reading process stderr",
					Error:   err})
			}
			// The add-tag has stderr; it uses stderr for debug messages, so
			// don't count it as an error. Instead just print it out as extra
			// info.
			sc.Infof("process stdout:\n%v\n", string(b))
			sc.Infof("process stderr:\n%v\n", string(be))
			err = req.StreamProducer.Close()
			if err != nil {
				errors = append(errors, Error{
					Context: "closing process",
					Error:   err})
			}
			reqRes.Errors = errors
			requestResults <- reqRes
		}
	}

	captured := make(CapturedRequests)

	if sc.DryRun {
		processRequestDryRun := MkRequestCapturer(&captured)
		processRequest = processRequestDryRun
	} else {
		processRequest = processRequestReal
	}

	if customProcessRequest != nil {
		processRequest = *customProcessRequest
	}
	sc.ExecRequests(populateRequests, processRequest)

	if sc.DryRun {
		sc.PrintCapturedRequests(&captured)
	}
}

// PrintCapturedRequests pretty-prints all given PromotionRequests.
func (sc *SyncContext) PrintCapturedRequests(capReqs *CapturedRequests) {
	prs := make([]PromotionRequest, 0)

	for req, count := range *capReqs {
		for i := 0; i < count; i++ {
			prs = append(prs, req)
		}
	}

	sort.Slice(prs, func(i, j int) bool {
		return prs[i].PrettyValue() < prs[j].PrettyValue()
	})
	if len(prs) > 0 {
		for _, pr := range prs {
			fmt.Printf("%v\n", pr.PrettyValue())
		}
	} else {
		fmt.Println("No requests generated.")
	}
}

// PrettyValue is a prettified string representation of a TagOp.
func (op *TagOp) PrettyValue() string {
	var tagOpPretty string
	switch *op {
	case Add:
		tagOpPretty = "ADD"
	case Move:
		tagOpPretty = "MOVE"
	case Delete:
		tagOpPretty = "DELETE"
	}
	return tagOpPretty
}

// PrettyValue is a prettified string representation of a PromotionRequest.
func (pr *PromotionRequest) PrettyValue() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%v -> %v: Tag: '%v' <%v> %v@%v",
		string(pr.Registries.Src),
		string(pr.Registries.Dest),
		string(pr.Tag),
		pr.TagOp.PrettyValue(),
		string(pr.ImageName),
		string(pr.Digest))
	if len(pr.DigestOld) > 0 {
		fmt.Fprintf(&b, " (move from '%v')", string(pr.DigestOld))
	}
	fmt.Fprintf(&b, "\n")

	return b.String()
}

// MkRequestCapturer returns a function that simply records requests as they are
// captured (slured out from the reqs channel).
func MkRequestCapturer(captured *CapturedRequests) ProcessRequest {
	return func(
		sc *SyncContext,
		reqs <-chan stream.ExternalRequest,
		errs chan<- RequestResult,
		wg *sync.WaitGroup,
		mutex *sync.Mutex) {

		defer wg.Done()

		for req := range reqs {
			pr := req.RequestParams.(PromotionRequest)
			mutex.Lock()
			if _, ok := (*captured)[pr]; ok {
				(*captured)[pr]++
			} else {
				(*captured)[pr] = 1
			}
			mutex.Unlock()
		}
	}
}

// GarbageCollect deletes all images that are not referenced by Docker tags.
func (sc *SyncContext) GarbageCollect(
	mfest Manifest,
	mkProducer func(RegistryName, ImageName, Digest) stream.Producer,
	customProcessRequest *ProcessRequest) {

	var populateRequests PopulateRequests = func(
		sc *SyncContext,
		reqs chan<- stream.ExternalRequest) {

		for imageName, digestTags := range sc.Inv[mfest.Registries.Dest] {
			for digest, tagArray := range digestTags {
				if len(tagArray) == 0 {
					var req stream.ExternalRequest
					req.StreamProducer = mkProducer(
						mfest.Registries.Dest,
						imageName,
						digest)
					req.RequestParams = PromotionRequest{
						Delete,
						mfest.Registries,
						imageName,
						digest,
						"",
						"",
					}
					reqs <- req
				}
			}
		}
	}

	var processRequest ProcessRequest
	var processRequestReal ProcessRequest = func(
		sc *SyncContext,
		reqs <-chan stream.ExternalRequest,
		requestResults chan<- RequestResult,
		wg *sync.WaitGroup,
		mutex *sync.Mutex) {

		defer wg.Done()
		for req := range reqs {
			reqRes := RequestResult{Context: req}
			jsons, errors := getJSONSFromProcess(req)
			if len(errors) > 0 {
				reqRes.Errors = errors
				requestResults <- reqRes
				continue
			}
			for _, json := range jsons {
				sc.Info("DELETED image:", json)
			}
			reqRes.Errors = errors
			requestResults <- reqRes
		}
	}

	captured := make(CapturedRequests)

	if sc.DryRun {
		processRequestDryRun := MkRequestCapturer(&captured)
		processRequest = processRequestDryRun
	} else {
		processRequest = processRequestReal
	}

	if customProcessRequest != nil {
		processRequest = *customProcessRequest
	}
	sc.ExecRequests(populateRequests, processRequest)

	if sc.DryRun {
		sc.PrintCapturedRequests(&captured)
	}
}

// GetWriteCmd generates a gcloud command that is used to make modifications to
// a Docker Registry.
func GetWriteCmd(
	serviceAccount string,
	srcRegistry RegistryName,
	destRegistry RegistryName,
	image ImageName,
	digest Digest,
	tag Tag,
	tp TagOp) []string {

	var cmd []string
	switch tp {
	case Move:
		// The "add-tag" command also moves tags as necessary.
		fallthrough
	case Add:
		cmd = []string{"gcloud",
			fmt.Sprintf("--account=%v", serviceAccount),
			"--quiet",
			"--verbosity=debug",
			"container",
			"images",
			"add-tag",
			ToFQIN(srcRegistry, image, digest),
			ToPQIN(destRegistry, image, tag)}
	case Delete:
		cmd = []string{"gcloud",
			fmt.Sprintf("--account=%v", serviceAccount),
			"--quiet",
			"container",
			"images",
			"untag",
			ToPQIN(destRegistry, image, tag)}
	}
	return cmd
}

// GetDeleteCmd generates the cloud command used to delete images (used for
// garbage collection).
func GetDeleteCmd(
	serviceAccount string,
	registryName RegistryName,
	img ImageName,
	digest Digest) []string {

	fqin := ToFQIN(registryName, img, digest)
	return []string{
		"gcloud",
		fmt.Sprintf("--account=%v", serviceAccount),
		"container",
		"images",
		"delete",
		fqin,
		"--format=json"}
}

// GetRegistryListingCmd generates the invocation for retrieving all images in a
// GCR.
func GetRegistryListingCmd(serviceAccount, r string) []string {
	return []string{
		"gcloud",
		fmt.Sprintf("--account=%v", serviceAccount),
		"container",
		"images",
		"list",
		fmt.Sprintf("--repository=%s", r), "--format=json"}
}

// GetRegistryListTagsCmd generates the invocation for retrieving all digests
// (and tags on them) for a given image.
func GetRegistryListTagsCmd(
	serviceAccount, registryName string, img string) []string {
	return []string{
		"gcloud",
		fmt.Sprintf("--account=%v", serviceAccount),
		"container",
		"images",
		"list-tags",
		fmt.Sprintf("%s/%s", registryName, img), "--format=json"}
}
