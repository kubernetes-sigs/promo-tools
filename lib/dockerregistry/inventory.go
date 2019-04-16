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
	"regexp"
	"sort"
	"strings"
	"sync"

	yaml "gopkg.in/yaml.v2"

	json "github.com/kubernetes-sigs/k8s-container-image-promoter/lib/json"
	"github.com/kubernetes-sigs/k8s-container-image-promoter/lib/stream"
)

func getSrcRegistry(rcs []RegistryContext) (*RegistryContext, error) {
	for _, registry := range rcs {
		registry := registry
		if registry.Src {
			return &registry, nil
		}
	}
	return nil, fmt.Errorf("could not find source registry")
}

// MakeSyncContext creates a SyncContext.
func MakeSyncContext(
	manifestPath string,
	rcs []RegistryContext,
	rd RenamesDenormalized,
	srcRegistry *RegistryContext,
	mi MasterInventory,
	verbosity, threads int,
	deleteExtraTags, dryRun, useSvcAcc bool) (SyncContext, error) {

	return SyncContext{
		Verbosity:           verbosity,
		Threads:             threads,
		DeleteExtraTags:     deleteExtraTags,
		ManifestPath:        manifestPath,
		DryRun:              dryRun,
		UseServiceAccount:   useSvcAcc,
		Inv:                 mi,
		RenamesDenormalized: rd,
		SrcRegistry:         srcRegistry,
		RegistryContexts:    rcs}, nil
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
func ParseManifestFromFile(
	filePath string) (Manifest, RenamesDenormalized, *RegistryContext, error) {

	var mfest Manifest
	var rd RenamesDenormalized
	var srcRegistry *RegistryContext
	bytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		return mfest, rd, srcRegistry, err
	}
	mfest, err = ParseManifest(bytes)
	if err != nil {
		return mfest, rd, srcRegistry, err
	}

	// Perform semantic checks (beyond just YAML validation).
	srcRegistry, err = getSrcRegistry(mfest.Registries)
	if err != nil {
		return mfest, rd, srcRegistry, err
	}

	rd, err = DenormalizeRenames(mfest, srcRegistry.Name)
	if err != nil {
		return mfest, rd, srcRegistry, err
	}

	return mfest, rd, srcRegistry, nil
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
	if err := validateRequiredComponents(m); err != nil {
		return err
	}
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

func validateRegistryImagePath(rip RegistryImagePath) error {
	validRegistryImagePath := regexp.MustCompile(
		// \w is [0-9a-zA-Z_]
		`^[\w-]+(\.[\w-]+)+(/[\w-]+)+$`)
	if !validRegistryImagePath.Match([]byte(rip)) {
		return fmt.Errorf("invalid registry image path: %v", rip)
	}
	return nil
}

func (m Manifest) srcRegistryCount() int {
	var count int
	for _, registry := range m.Registries {
		if registry.Src {
			count++
		}
	}
	return count
}

func (m Manifest) srcRegistryName() RegistryName {
	for _, registry := range m.Registries {
		if registry.Src {
			return registry.Name
		}
	}
	return RegistryName("")
}

// nolint[gocyclo]
func validateRequiredComponents(m Manifest) error {
	errs := make([]string, 0)
	srcRegistryName := RegistryName("")
	if len(m.Registries) > 0 {
		if m.srcRegistryCount() > 1 {
			errs = append(errs, fmt.Sprintf("cannot have more than 1 source registry"))
		}
		srcRegistryName = m.srcRegistryName()
		if len(srcRegistryName) == 0 {
			errs = append(errs, fmt.Sprintf("source registry must be set"))
		}
	}

	if len(m.Registries) == 0 {
		errs = append(errs, fmt.Sprintf("'registries' field cannot be empty"))
	}
	for _, registry := range m.Registries {
		if len(registry.Name) == 0 {
			errs = append(
				errs,
				fmt.Sprintf("registries: 'name' field cannot be empty"))
		}
	}
	for _, image := range m.Images {
		if len(image.ImageName) == 0 {
			errs = append(
				errs,
				fmt.Sprintf("images: 'name' field cannot be empty"))
		}
		if len(image.Dmap) == 0 {
			errs = append(
				errs,
				fmt.Sprintf("images: 'dmap' field cannot be empty"))
		}
	}

	for _, rename := range m.Renames {
		// The names must be valid paths.
		// Each name must be the registry+pathname, *without* a trailing slash.
		if len(rename) < 2 {
			// nolint[lll]
			errs = append(errs,
				fmt.Sprintf("a rename entry must have at least 2 paths, one for the source, another for at least 1 dest registry"))
		}

		// Make sure there is only 1 rename per registry, and that there is 1
		// entry for the source registry..
		var srcOriginal RegistryImagePath
		seenRegistries := make(map[RegistryName]ImageName)
		for _, registryImagePath := range rename {
			// nolint[lll]
			registryName, imageName, err := splitRegistryImagePath(registryImagePath)
			if err != nil {
				errs = append(errs, err.Error())
			}

			if _, ok := seenRegistries[registryName]; ok {
				// nolint[lll]
				errs = append(errs, fmt.Sprintf("multiple renames found for registry '%v' in 'renames', for image %v",
					registryName,
					imageName))
			} else {
				seenRegistries[registryName] = imageName
			}

			if err = validateRegistryImagePath(registryImagePath); err != nil {
				errs = append(errs, err.Error())
			}

			// Check if the registry is found in the outer `registries` field.
			found := false
			for _, rc := range m.Registries {
				if rc.Name == registryName {
					found = true
				}
			}
			if !found {
				// nolint[lll]
				errs = append(errs, fmt.Sprintf("unknown registry '%v' in 'renames' (not defined in 'registries')", registryName))
			}

			if registryName == srcRegistryName {
				srcOriginal = registryImagePath
			}
		}

		if len(m.Registries) > 0 && len(srcOriginal) == 0 {
			errs = append(errs, fmt.Sprintf("could not find source registry in '%v'",
				rename))
		}

		registryNameSrc, imageNameSrc, _ := splitRegistryImagePath(srcOriginal)
		for _, registryImagePath := range rename {
			registryName, imageName, _ := splitRegistryImagePath(registryImagePath)
			if registryName != registryNameSrc {
				if imageName == imageNameSrc {
					errs = append(errs, fmt.Sprintf("redundant rename for %s",
						rename))
				}
			}
		}

	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf(strings.Join(errs, "\n"))
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
	mkProducer func(RegistryContext) stream.Producer) {
	// Collect all images in sc.Inv (the src and dest reqgistry names found in
	// the manifest).
	var populateRequests PopulateRequests = func(
		sc *SyncContext, reqs chan<- stream.ExternalRequest) {

		for _, rc := range sc.RegistryContexts {
			var req stream.ExternalRequest
			req.RequestParams = rc.Name
			req.StreamProducer = mkProducer(rc)
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
	mkProducer func(RegistryContext, ImageName) stream.Producer) {

	var populateRequests PopulateRequests = func(
		sc *SyncContext,
		reqs chan<- stream.ExternalRequest) {

		for registryName, imagesMap := range sc.Inv {
			for _, rc := range sc.RegistryContexts {
				if registryName == rc.Name {
					for imgName := range imagesMap {
						var req stream.ExternalRequest
						req.StreamProducer = mkProducer(rc, imgName)
						req.RequestParams = DigestTagsContext{
							ImageName:    imgName,
							RegistryName: registryName}
						reqs <- req
					}
					break
				}
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

// ToLQIN converts a RegistryName and ImangeName to form a loosely-qualified
// image name (LQIN). Notice that it is missing tag information --- hence
// "loosely-qualified".
func ToLQIN(registryName RegistryName, imageName ImageName) string {
	return string(registryName) + "/" + string(imageName)
}

// GetLostImages gets all images in Manifest which are missing from src, and
// also logs them in the process.
func (sc *SyncContext) GetLostImages(mfest Manifest) RegInvImageDigest {
	src := sc.Inv[sc.SrcRegistry.Name].ToRegInvImageDigest()

	// lost = all images that cannot be found from src.
	lost := mfest.ToRegInvImageDigest().Minus(src)
	if len(lost) > 0 {
		sc.Errorf(
			"ERROR: Lost images (all images in Manifest that cannot be found"+
				" from src registry %v):\n",
			sc.SrcRegistry.Name)
		for imageDigest := range lost {
			fqin := ToFQIN(
				sc.SrcRegistry.Name,
				imageDigest.ImageName,
				imageDigest.Digest)
			sc.Errorf(
				"  %v in manifest is NOT in src registry!\n",
				fqin)
		}
		return lost
	}
	return nil
}

// NOTE: the issue with renaming an image is that we still need to know the
// original (source) image path because we need to use it in the actual call to
// add-tag during promotion. This means the request population logic must be
// aware of both the original and renamed version of the image name.
//
// By the time the mkPopReq is called, we are dealing with renamed inventory
// (riit). We need to detect this case from within this function and create the
// correct request populator --- namely, the source registry/Manifest's original
// image name must be used as the reference.
//
// The Manifest houses `registries` which contain the registry names. The
// RenamesDenormalized map will contain bidirectional entries for the rename.
// Since we already have the dest registry, we can just look up the destRegistry
// name + (renamed) image name in the RenamesDenormalized map; if we find an
// entry, then we just need to pick out the one for the source registry (it is
// guaranteed to be there). Now we have the source registry name in sc, so we
// can use that and the image name associated with the source registry to use as
// the source destination reg+image name.
func (sc *SyncContext) mkPopReq(
	destRC RegistryContext,
	riit RegInvImageTag,
	tp TagOp,
	oldDigest Digest,
	mkProducer PromotionContext,
	reqs chan<- stream.ExternalRequest) {

	dest := sc.Inv[destRC.Name]
	for imageTag, digest := range riit {
		var req stream.ExternalRequest

		// Get original image name, if we detect a rename.
		srcImageName := imageTag.ImageName
		registryImagePath := RegistryImagePath(
			ToLQIN(destRC.Name, imageTag.ImageName))
		if renameMap, ok := sc.RenamesDenormalized[registryImagePath]; ok {
			if origImageName, ok := renameMap[sc.SrcRegistry.Name]; ok {
				srcImageName = origImageName
			} else {
				// This should never happen, as the src registry is guaranteed
				// (during manifest parsing and renames denormalization) to
				// exist as a key for every map in RenamesDenormalized.
				//
				// nolint[lll]
				sc.Warnf("could not find src registry in renameMap for image '%v'\n", imageTag.ImageName)
				continue
			}
		}

		req.StreamProducer = mkProducer(
			// We need to populate from the source to the dest.
			sc.SrcRegistry.Name,
			srcImageName,
			destRC,
			imageTag.ImageName,
			digest,
			imageTag.Tag,
			tp)
		tpStr := ""
		fqin := ToFQIN(
			destRC.Name,
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
			"mkPopReq: %s tag: %s -> %s\n", tpStr, imageTag.Tag, fqin)
		// For moving tags, display a more verbose message (show the
		// what the tag currently points to).
		if tp == Move {
			msg = fmt.Sprintf(`%s tag: %s
  OLD -> %s/%s@%s
  NEW -> %s/%s@%s
`,
				tpStr,
				imageTag.Tag,
				destRC.Name,
				imageTag.ImageName,
				oldDigest,
				destRC.Name,
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
			sc.SrcRegistry.Name,
			destRC.Name,
			destRC.ServiceAccount,
			srcImageName,
			imageTag.ImageName,
			digest,
			oldDigest,
			imageTag.Tag,
		}
		reqs <- req
	}

}

func splitRegistryImagePath(
	registryImagePath RegistryImagePath) (RegistryName, ImageName, error) {

	pathSlice := strings.SplitAfterN(string(registryImagePath), "/", 2)
	if len(pathSlice) != 2 {
		goto error
	}

	// For "gcr.io/a/b/c", the registry name is "gcr.io/a", not "gcr.io/". So
	// fix it.

	if pathSlice[0] == "gcr.io/" {
		pathSliceGCR := strings.SplitAfterN(pathSlice[1], "/", 2)
		if len(pathSliceGCR) != 2 {
			goto error
		}
		projectName := pathSliceGCR[0]
		return RegistryName(pathSlice[0] + projectName[0:len(projectName)-1]),
			ImageName(pathSliceGCR[1]),
			nil
	}

	return RegistryName(pathSlice[0]), ImageName(pathSlice[1]), nil

error:
	return RegistryName(""),
		ImageName(""),
		// nolint[lll]
		fmt.Errorf("invalid loosely-qualified image name '%v'", registryImagePath)
}

// DenormalizeRenames coverts the nested list of rename strings in the
// Manifest's `renames` field into a more query-friendly nested map (easier to
// perform lookups).
//
// It also checks the `renames` field in Manifest for errors.
// nolint[gocyclo]
func DenormalizeRenames(
	mfest Manifest,
	srcRegistryName RegistryName) (RenamesDenormalized, error) {

	rd := make(RenamesDenormalized)
	for _, rename := range mfest.Renames {
		// Create "directed edges" that go in both directions --- from Src
		// to Dest, and Dest to Src. Because there can be multiple
		// destinations, we have to use a nested loop.
		for i, registryImagePathA := range rename {
			_, _, err := splitRegistryImagePath(registryImagePathA)
			if err != nil {
				return nil, err
			}

			// Create the directed edge targets.
			directedEdges := make(map[RegistryName]ImageName)
			for j, registryImagePathB := range rename {
				if j == i {
					continue
				}

				registryNameB, imageNameB, err := splitRegistryImagePath(registryImagePathB)
				if err != nil {
					return nil, err
				}
				directedEdges[registryNameB] = imageNameB
			}

			// Assign all targets to the starting point, registryImagePathA.
			rd[registryImagePathA] = directedEdges
		}
	}
	return rd, nil
}

func getRenameMap(
	lqin string,
	rd RenamesDenormalized) map[RegistryName]ImageName {

	m, ok := rd[RegistryImagePath(lqin)]
	if ok {
		return m
	}
	return nil
}

// applyRenames takes a given RegInvImageTag (riit) and converts it to a new
// RegInvImageTag (riitRenamed), by using information in RenamesDenormalized
// (rd).
func (riit *RegInvImageTag) applyRenames(
	srcReg RegistryName,
	destReg RegistryName,
	rd RenamesDenormalized) RegInvImageTag {

	riitRenamed := RegInvImageTag{}
	for imageTag, digest := range *riit {
		lqin := ToLQIN(srcReg, imageTag.ImageName)
		m := getRenameMap(lqin, rd)
		newName, ok := m[destReg]
		// Only rename if this dest registry requires renaming.
		if m != nil && ok {
			imageTagRenamed := imageTag
			imageTagRenamed.ImageName = newName
			riitRenamed[imageTagRenamed] = digest
		} else {
			riitRenamed[imageTag] = digest
		}
	}
	return riitRenamed
}

// mkPopulateRequestsForPromotion creates all requests necessary to reconcile
// the Manifest against the state of the world.
//
// nolint[gocyclo]
func mkPopulateRequestsForPromotion(
	mfest Manifest,
	promotionCandidatesIT RegInvImageTag,
	mkProducer PromotionContext) PopulateRequests {
	return func(sc *SyncContext, reqs chan<- stream.ExternalRequest) {
		for _, registry := range mfest.Registries {
			if registry.Src {
				continue
			}

			// Promote images that are not in the destination registry.
			destIT := sc.Inv[registry.Name].ToRegInvImageTag()

			// For this dest registry, check if there are any renamings that
			// must occur. If so, modify the promotionCandidatesIT so that they
			// use the renamed versions. This is because destIT already has the
			// renamed versions (easier for set difference). We could go the
			// other way and rename the pertinent images in destIT to match the
			// naming scheme of the source registry (i.e., the raw `images`
			// field in the Manifest) --- one way is not obviously superior over
			// the other.
			promotionCandidatesITRenamed := promotionCandidatesIT.applyRenames(
				sc.SrcRegistry.Name, registry.Name, sc.RenamesDenormalized)

			promotionFiltered := promotionCandidatesITRenamed.Minus(destIT)
			promotionFilteredID := promotionFiltered.ToRegInvImageDigest()

			if len(promotionFilteredID) > 0 {
				sc.Infof(
					"To promote (after removing already-promoted images):\n%v",
					promotionFilteredID.PrettyValue())
				if sc.DryRun {
					sc.Infof(
						"---------- BEGIN PROMOTION (DRY RUN): %s: %s ----------\n",
						sc.ManifestPath,
						registry.Name)
				} else {
					sc.Infof("---------- BEGIN PROMOTION: %s: %s ----------\n",
						sc.ManifestPath,
						registry.Name)
				}
			} else {
				sc.Infof("Nothing to promote for %s.\n", registry.Name)
			}

			sc.mkPopReq(
				registry, // destRegistry
				promotionFiltered,
				Add,
				"",
				mkProducer,
				reqs)

			// Audit the intersection (make sure already-existing tags in the
			// destination are pointing to the digest specified in the
			// manifest).
			toAudit := promotionCandidatesITRenamed.Intersection(destIT)
			for imageTag, digest := range toAudit {
				if liveDigest, ok := destIT[imageTag]; ok {
					// dest has this imageTag already; need to audit.
					pqin := ToPQIN(
						registry.Name,
						imageTag.ImageName,
						imageTag.Tag)
					if digest == liveDigest {
						// NOP if dest's imageTag is already pointing to the
						// same digest as in the manifest.

						sc.Infof(
							"skipping: image '%v' already points to the same"+
								" digest (%v) as in the Manifest\n",
							pqin,
							digest)
						continue
					} else {
						// Dest's tag is pointing to the wrong digest! Need to
						// move the tag.
						sc.Warnf(
							"Warning: image '%v' is pointing to the wrong"+
								" digest\n       got: %v\n  expected: %v\n",
							pqin,
							liveDigest,
							digest)
						sc.mkPopReq(
							registry,
							RegInvImageTag{imageTag: digest},
							Move,
							liveDigest,
							mkProducer,
							reqs)
					}
				}
			}

			// Either delete extraneous tags in the destination, or warn about
			// them (hinges on sc.DeleteExtraTags).
			mfestIT := mfest.ToRegInvImageTag()
			if sc.DeleteExtraTags {
				sc.mkPopReq(
					registry,
					destIT.Minus(mfestIT),
					Delete,
					"",
					mkProducer,
					reqs)
			} else {
				// Warn the user about extra tags:
				xtras := make([]string, 0)
				for imageTag := range destIT.Minus(promotionCandidatesITRenamed) {
					xtras = append(xtras, fmt.Sprintf(
						"%s/%s:%s",
						registry.Name,
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
}

// GetPromotionCandidatesIT returns those images that are due for promotion.
func (sc *SyncContext) GetPromotionCandidatesIT(
	mfest Manifest) RegInvImageTag {

	src := sc.Inv[sc.SrcRegistry.Name]

	// promotionCandidates = all images in the manifest that can be found from
	// src. But, this is filtered later to remove those ones that are already in
	// dest (see mkPopulateRequestsForPromotion).
	promotionCandidates := mfest.ToRegInvImageDigest().Intersection(
		src.ToRegInvImageDigest())
	sc.Infof(
		"To promote (intersection of Manifest and src registry %v):\n%v",
		sc.SrcRegistry.Name,
		promotionCandidates.PrettyValue())

	return promotionCandidates.ToRegInvImageTag()
}

// Promote perferms container image promotion by realizing the intent in the
// Manifest.
func (sc *SyncContext) Promote(
	mfest Manifest,
	mkProducer func(
		RegistryName,
		ImageName,
		RegistryContext,
		ImageName,
		Digest,
		Tag,
		TagOp) stream.Producer,
	customProcessRequest *ProcessRequest) int {

	var exitCode int

	mfestID := (mfest.ToRegInvImageDigest())

	sc.Infof("Desired state:\n%v", mfestID.PrettyValue())
	lost := sc.GetLostImages(mfest)
	if len(lost) > 0 {
		// TODO: Have more meaningful exit codes (use iota?).
		exitCode = 1
	}

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

	return exitCode
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
		fmt.Println("")
		fmt.Println("captured reqs summary:")
		fmt.Println("")
		for _, pr := range prs {
			fmt.Printf("captured req: %v", pr.PrettyValue())
		}
		fmt.Println("")
	} else {
		fmt.Println("No requests captured.")
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
	fmt.Fprintf(&b, "%v -> %v: Tag: '%v' <%v> %v",
		ToLQIN(pr.RegistrySrc, pr.ImageNameSrc),
		ToLQIN(pr.RegistryDest, pr.ImageNameDest),
		string(pr.Tag),
		pr.TagOp.PrettyValue(),
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
// nolint[gocyclo]
func (sc *SyncContext) GarbageCollect(
	mfest Manifest,
	mkProducer func(RegistryContext, ImageName, Digest) stream.Producer,
	customProcessRequest *ProcessRequest) {

	var populateRequests PopulateRequests = func(
		sc *SyncContext,
		reqs chan<- stream.ExternalRequest) {

		for _, registry := range mfest.Registries {
			if registry.Name == sc.SrcRegistry.Name {
				continue
			}
			for imageName, digestTags := range sc.Inv[registry.Name] {
				for digest, tagArray := range digestTags {
					if len(tagArray) == 0 {
						var req stream.ExternalRequest
						req.StreamProducer = mkProducer(
							registry,
							imageName,
							digest)
						req.RequestParams = PromotionRequest{
							Delete,
							sc.SrcRegistry.Name,
							registry.Name,
							registry.ServiceAccount,

							// No source image name, because tag deletions
							// should only delete the what's in the
							// destination registry
							ImageName(""),

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

// MaybeUseServiceAccount injects a '--account=...' argument to the command with
// the given service account.
func MaybeUseServiceAccount(
	serviceAccount string,
	useServiceAccount bool,
	cmd []string) []string {
	if useServiceAccount && len(serviceAccount) > 0 {
		cmd = append(cmd, "")
		copy(cmd[2:], cmd[1:])
		cmd[1] = fmt.Sprintf("--account=%v", serviceAccount)
	}
	return cmd
}

// GetWriteCmd generates a gcloud command that is used to make modifications to
// a Docker Registry.
func GetWriteCmd(
	dest RegistryContext,
	useServiceAccount bool,
	srcRegistry RegistryName,
	srcImageName ImageName,
	destImageName ImageName,
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
			"--quiet",
			"--verbosity=debug",
			"container",
			"images",
			"add-tag",
			ToFQIN(srcRegistry, srcImageName, digest),
			ToPQIN(dest.Name, destImageName, tag)}
	case Delete:
		cmd = []string{"gcloud",
			"--quiet",
			"container",
			"images",
			"untag",
			ToPQIN(dest.Name, destImageName, tag)}
	}
	// Use the service account if it is desired.
	return MaybeUseServiceAccount(dest.ServiceAccount, useServiceAccount, cmd)
}

// GetDeleteCmd generates the cloud command used to delete images (used for
// garbage collection).
func GetDeleteCmd(
	rc RegistryContext,
	useServiceAccount bool,
	img ImageName,
	digest Digest) []string {

	fqin := ToFQIN(rc.Name, img, digest)
	cmd := []string{
		"gcloud",
		"container",
		"images",
		"delete",
		fqin,
		"--format=json"}
	return MaybeUseServiceAccount(rc.ServiceAccount, useServiceAccount, cmd)
}

// GetRegistryListingCmd generates the invocation for retrieving all images in a
// GCR.
func GetRegistryListingCmd(
	rc RegistryContext,
	useServiceAccount bool) []string {
	cmd := []string{
		"gcloud",
		"container",
		"images",
		"list",
		fmt.Sprintf("--repository=%s", rc.Name), "--format=json"}
	return MaybeUseServiceAccount(rc.ServiceAccount, useServiceAccount, cmd)
}

// GetRegistryListTagsCmd generates the invocation for retrieving all digests
// (and tags on them) for a given image.
func GetRegistryListTagsCmd(
	rc RegistryContext,
	useServiceAccount bool,
	img string) []string {
	cmd := []string{
		"gcloud",
		"container",
		"images",
		"list-tags",
		fmt.Sprintf("%s/%s", rc.Name, img), "--format=json"}
	return MaybeUseServiceAccount(rc.ServiceAccount, useServiceAccount, cmd)
}
