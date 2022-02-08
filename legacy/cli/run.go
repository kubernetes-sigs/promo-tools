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

package cli

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	reg "sigs.k8s.io/promo-tools/v3/legacy/dockerregistry"
	"sigs.k8s.io/promo-tools/v3/legacy/gcloud"
	"sigs.k8s.io/promo-tools/v3/legacy/stream"
)

type RunOptions struct {
	Manifest                string
	ThinManifestDir         string
	KeyFiles                string
	Snapshot                string
	SnapshotTag             string
	OutputFormat            string
	SnapshotSvcAcct         string
	ManifestBasedSnapshotOf string

	// TODO: Review/optimize/de-dupe (https://github.com/kubernetes-sigs/promo-tools/pull/351)
	CheckManifestLists string

	// TODO: Review/optimize/de-dupe (https://github.com/kubernetes-sigs/promo-tools/pull/351)
	Repository string

	Threads           int
	MaxImageSize      int
	SeverityThreshold int
	Confirm           bool
	JSONLogSummary    bool
	ParseOnly         bool
	MinimalSnapshot   bool
	UseServiceAcct    bool
}

const (
	PromoterDefaultThreads           = 10
	PromoterDefaultOutputFormat      = "yaml"
	PromoterDefaultMaxImageSize      = 2048
	PromoterDefaultSeverityThreshold = -1

	// flags.
	PromoterManifestFlag                = "manifest"
	PromoterThinManifestDirFlag         = "thin-manifest-dir"
	PromoterSnapshotFlag                = "snapshot"
	PromoterManifestBasedSnapshotOfFlag = "manifest-based-snapshot-of"
	PromoterOutputFlag                  = "output"
)

var PromoterAllowedOutputFormats = []string{
	"csv",
	"yaml",
}

// TODO: Function 'runPromoteCmd' has too many statements (97 > 40) (funlen)
// nolint: funlen,gocognit,gocyclo
func RunPromoteCmd(opts *RunOptions) error {
	// NOTE(@puerco): Now available to all modes in impl.ValidateOptions
	if err := validateImageOptions(opts); err != nil {
		return errors.Wrap(err, "validating image options")
	}

	// Activate service accounts.

	// TODO: Move this into the validation function
	// NOTE(@puerco): Available to all modes in impl.ActivateServiceAccounts
	if opts.UseServiceAcct && opts.KeyFiles != "" {
		if err := gcloud.ActivateServiceAccounts(opts.KeyFiles); err != nil {
			return errors.Wrap(err, "activating service accounts")
		}
	}

	// TODO: Move this into the validation function
	// NOTE(@puerco): This code path ends here. no snapshot, sec scan or promo.
	// It this is the main checking function, we need to deprecate this running
	// in promotion and create a subcommand for it
	if opts.CheckManifestLists != "" {
		if opts.Repository == "" {
			logrus.Fatalf("a repository must be specified when checking manifest lists")
		}

		return validateManifestLists(opts)
	}

	var (
		mfest       reg.Manifest
		srcRegistry *reg.RegistryContext
		err         error
		mfests      []reg.Manifest
	)

	promotionEdges := make(map[reg.PromotionEdge]interface{})
	sc := reg.SyncContext{}
	mi := make(reg.MasterInventory)

	// TODO: Move this into the validation function
	/// NOTE(@puerco): This is only for snapshots. No promo or sec scan
	///
	/// This part now lives in impl.GetSnapshotManifests
	if opts.Snapshot != "" || opts.ManifestBasedSnapshotOf != "" {
		if opts.Snapshot != "" {
			srcRegistry = &reg.RegistryContext{
				Name:           reg.RegistryName(opts.Snapshot),
				ServiceAccount: opts.SnapshotSvcAcct,
				Src:            true,
			}
		} else {
			srcRegistry = &reg.RegistryContext{
				Name:           reg.RegistryName(opts.ManifestBasedSnapshotOf),
				ServiceAccount: opts.SnapshotSvcAcct,
				Src:            true,
			}
		}

		mfests = []reg.Manifest{
			{
				Registries: []reg.RegistryContext{
					*srcRegistry,
				},
				Images: []reg.Image{},
			},
		}
		// TODO: Move this into the validation function
	} else if opts.Manifest == "" && opts.ThinManifestDir == "" {
		// this is in the options check.
		logrus.Fatalf(
			"either %s or %s flag is required",
			PromoterManifestFlag,
			PromoterThinManifestDirFlag,
		)
	}

	// NOTE(@puerco): This flag determines if we enter the promotion
	// path.
	// if ThinManifestDir or Manifest are set, its set to true
	doingPromotion := false

	// NOTE(@puerco):
	// The following two conditions are replaced by impl.ParseMenifests
	// and MakeSyncContext
	// one thing to note here (I think it's a bug) is that if a manifest file
	// is set in opts.Manifest, it gets appended to the snaphot manifests
	// set in the previous condition.
	//
	// Since the original code merged the manifests, we keep that logic
	// in the refactor in impl.AppendManifestToSnapshot(). Note that
	// if a thin manifests dir is specified it is redefined completely, not
	// appended. Also of note, the SyncContext here is also not used when
	// running a snapshot.
	//
	// TODO: is deeply nested (complexity: 5) (nestif)
	// nolint: nestif
	if opts.Manifest != "" {
		mfest, err = reg.ParseManifestFromFile(opts.Manifest)
		if err != nil {
			logrus.Fatal(err)
		}

		mfests = append(mfests, mfest)
		for _, registry := range mfest.Registries {
			mi[registry.Name] = nil
		}

		sc, err = reg.MakeSyncContext(
			mfests,
			opts.Threads,
			opts.Confirm,
			opts.UseServiceAcct,
		)
		if err != nil {
			logrus.Fatal(err)
		}

		doingPromotion = true
	} else if opts.ThinManifestDir != "" {
		mfests, err = reg.ParseThinManifestsFromDir(opts.ThinManifestDir)
		if err != nil {
			return errors.Wrap(err, "parsing thin manifest directory")
		}

		sc, err = reg.MakeSyncContext(
			mfests,
			opts.Threads,
			opts.Confirm,
			opts.UseServiceAcct,
		)
		if err != nil {
			logrus.Fatal(err)
		}

		doingPromotion = true
	}

	// this is an additional possible subcommand: parseonly
	if opts.ParseOnly {
		return nil
	}

	// If there are no images in the manifest, it may be a stub manifest file
	// (such as for brand new registries that would be watched by the promoter
	// for the very first time).
	//
	// NOTE(@puerco):
	// If we enter this loop. It means that:
	//   1. Either opts.Manifest or opts.ThinManifestDir are set
	//      thus causing doingPromotion == true. ie we are in a promotion
	//   2. Snapshot may or may be not set. If it's set, then the promot
	//      promo code will not run.
	//
	// If we enter this path, two (real) things will happen as the conditions in
	// the if clause that apply to other modes of operation are only for
	// printing log messages, the program version ,etc
	//
	//   1. promotionEdges is set set
	//   2. Some checks are performed to check if there are images in
	//      the manifests.
	//
	// - Promotion Edges will now be done in impl.GetPromotionEdges
	//
	// - The checks for images will now be done anyhow in a dedicated
	// function: impl.CheckImagesInManifests
	//
	// This code path runs if any of opts.Manifest or opts.ThinManifestDir
	// are set but regardeless if Snapshot is set
	// Yet to determine if this has any effect when running the snapshot
	//
	// Generating the edges is also done when running ManifestBasedSnapshotOf
	// bellow, so it can be refactored to a common function for snashots
	// and promotion
	//
	//
	//  TLDR: From this path, we only split the check code to another
	//        function which should run only when snapshotting. Phew.
	//
	// TODO: is deeply nested (complexity: 6) (nestif)
	// nolint: nestif
	if doingPromotion && opts.ManifestBasedSnapshotOf == "" {
		promotionEdges, err = reg.ToPromotionEdges(mfests)
		if err != nil {
			return errors.Wrap(
				err,
				"converting list of manifests to edges for promotion",
			)
		}

		imagesInManifests := false
		for _, mfest := range mfests {
			if len(mfest.Images) > 0 {
				imagesInManifests = true
				break
			}
		}
		if !imagesInManifests {
			logrus.Info("No images in manifest(s) --- nothing to do.")
			return nil
		}

		// Print version to make Prow logs more self-explanatory.
		printVersion()

		if opts.SeverityThreshold >= 0 {
			logrus.Info("********** START (VULN CHECK) **********")
			logrus.Info(
				`DISCLAIMER: Vulnerabilities are found as issues with package
binaries within image layers, not necessarily with the image layers themselves.
So a 'fixable' vulnerability may not necessarily be immediately actionable. For
example, even though a fixed version of the binary is available, it doesn't
necessarily mean that a new version of the image layer is available.`,
			)
		} else if opts.Confirm {
			logrus.Info("********** START **********")
		} else {
			logrus.Info("********** START (DRY RUN) **********")
		}
	}

	// NOTE(@puerco): The following path ends here. It's the snapshot. It runs if
	// opts.Snapshot or ManifestBasedSnapshotOf are set. This should be
	// another subcommand.
	//
	// The snapshot consits of two parts: generating a reg.RegInvImage
	// from two possible origins (manifest or thin manifest dir) and the
	// actual snapshot which is simply dumping the RegInvImage to
	// json, yaml or csv

	// TODO: is deeply nested (complexity: 12) (nestif)
	// nolint: nestif
	//
	// NOTE(@puerco): The firsat part now lives in impl.GetRegistryImageInventory
	if len(opts.Snapshot) > 0 || len(opts.ManifestBasedSnapshotOf) > 0 {
		rii := make(reg.RegInvImage)
		if len(opts.ManifestBasedSnapshotOf) > 0 {
			promotionEdges, err = reg.ToPromotionEdges(mfests)
			if err != nil {
				return errors.Wrap(
					err,
					"converting list of manifests to edges for promotion",
				)
			}

			rii = reg.EdgesToRegInvImage(
				promotionEdges,
				opts.ManifestBasedSnapshotOf,
			)

			if opts.MinimalSnapshot {
				sc.ReadRegistries(
					[]reg.RegistryContext{*srcRegistry},
					true,
					reg.MkReadRepositoryCmdReal,
				)

				sc.ReadGCRManifestLists(reg.MkReadManifestListCmdReal)
				rii = sc.RemoveChildDigestEntries(rii)
			}
		} else {
			sc, err = reg.MakeSyncContext(
				mfests,
				opts.Threads,
				opts.Confirm,
				opts.UseServiceAcct,
			)
			if err != nil {
				logrus.Fatal(err)
			}

			sc.ReadRegistries(
				[]reg.RegistryContext{*srcRegistry},
				// Read all registries recursively, because we want to produce a
				// complete snapshot.
				true,
				reg.MkReadRepositoryCmdReal,
			)

			rii = sc.Inv[mfests[0].Registries[0].Name]
			if opts.SnapshotTag != "" {
				rii = reg.FilterByTag(rii, opts.SnapshotTag)
			}

			if opts.MinimalSnapshot {
				logrus.Info("removing tagless child digests of manifest lists")
				sc.ReadGCRManifestLists(reg.MkReadManifestListCmdReal)
				rii = sc.RemoveChildDigestEntries(rii)
			}
		}

		// The snapshot will now be generated in
		// impl.Snapshot
		var snapshot string
		switch strings.ToLower(opts.OutputFormat) {
		case "csv":
			snapshot = rii.ToCSV()
		case "yaml":
			snapshot = rii.ToYAML(reg.YamlMarshalingOpts{})
		default:
			logrus.Errorf(
				"invalid value %s for '--%s'; defaulting to %s",
				opts.OutputFormat,
				PromoterOutputFlag,
				PromoterDefaultOutputFormat,
			)

			snapshot = rii.ToYAML(reg.YamlMarshalingOpts{})
		}

		fmt.Println(snapshot)
		return nil
	}

	// Option summary applies to everything except snapshots
	if opts.JSONLogSummary {
		defer sc.LogJSONSummary()
	}

	// this check here runs when confirm is set. This means
	// that here it would apply to security scan and promotion

	// Check the pull request
	if !opts.Confirm {
		err = sc.RunChecks([]reg.PreCheck{})
		if err != nil {
			return errors.Wrap(err, "running prechecks before promotion")
		}
	}

	// This comment says "Promote.", but its really not it.
	// actual promotion is further down
	//
	// This builds the function that will handle it

	// Promote.
	mkProducer := func(
		srcRegistry reg.RegistryName,
		srcImageName reg.ImageName,
		destRC reg.RegistryContext,
		imageName reg.ImageName,
		digest reg.Digest, tag reg.Tag, tp reg.TagOp,
	) stream.Producer {
		var sp stream.Subprocess
		sp.CmdInvocation = reg.GetWriteCmd(
			destRC,
			sc.UseServiceAccount,
			srcRegistry,
			srcImageName,
			imageName,
			digest,
			tag,
			tp,
		)

		return &sp
	}

	// TODO: Implement this in impl
	// NOTE(@puerco): This filter call will now live inside impl.GetPromotionEdges
	// and will run when we create them
	promotionEdges, ok := sc.FilterPromotionEdges(promotionEdges, true)
	// If any funny business was detected during a comparison of the manifests
	// with the state of the registries, then exit immediately.
	if !ok {
		return errors.New("encountered errors during edge filtering")
	}

	// This is the main path separating security scans and
	// the actual promotion. This is the scan
	if opts.SeverityThreshold >= 0 {
		err = sc.RunChecks(
			[]reg.PreCheck{
				reg.MKImageVulnCheck(
					&sc,
					promotionEdges,
					opts.SeverityThreshold,
					nil,
				),
			},
		)
		if err != nil {
			return errors.Wrap(err, "checking image vulnerabilities")
		}

	} else {

		// This is the actual call to promotion
		err = sc.Promote(promotionEdges, mkProducer, nil)
		if err != nil {
			return errors.Wrap(err, "promoting images")
		}
	}

	if opts.SeverityThreshold >= 0 {
		logrus.Info("********** FINISHED (VULN CHECK) **********")
	} else if opts.Confirm {
		logrus.Info("********** FINISHED **********")
	} else {
		logrus.Info("********** FINISHED (DRY RUN) **********")
	}

	return nil
}

func validateImageOptions(o *RunOptions) error {
	// TODO: Validate options
	return nil
}

// validateManifestLists ONLY reads yaml at the moment
// TODO: Review/optimize/de-dupe (https://github.com/kubernetes-sigs/promo-tools/pull/351)
func validateManifestLists(opts *RunOptions) error {
	pathToSnapshot := opts.CheckManifestLists
	registry := reg.RegistryName(opts.Repository)
	images := make([]reg.ImageWithDigestSlice, 0)
	err := reg.ParseSnapshot(pathToSnapshot, &images)
	if err != nil {
		return err
	}

	imgs, err := reg.FilterParentImages(registry, &images)
	if err != nil {
		return err
	}

	reg.ValidateParentImages(registry, imgs)
	fmt.Println("FINISHED")
	return nil
}
