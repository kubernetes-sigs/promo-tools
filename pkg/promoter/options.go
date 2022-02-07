package promoter

import (
	"errors"
)

// Options capture the switches available to run the image promoter
type Options struct {
	// Threads determines how many promotion threads will run
	Threads int

	// Confirm captures a cli flag with the same name. It runs the security
	// scan and promotion when set. If false, the promoter will exit before\
	// making any modifications.
	Confirm bool

	// Use a service account when true
	UseServiceAcct bool

	// Manifest is the path of a manifest file
	Manifest string

	// ThinManifestDir is a directory of thin manifests
	ThinManifestDir string

	// Snapshot takes a registry reference and renders a textual representation of
	// how the imagtes stored there look like to the promoter.
	Snapshot string

	// SnapshotSvcAcct is the service account we use when snapshotting.
	// TODO(puerco): Check as we can simplify to just one account
	SnapshotSvcAcct string

	// ManifestBasedSnapshotOf performs a snapshot from the given manifests
	// as opposed of Snapshot which will snapshot a registry across the network
	ManifestBasedSnapshotOf string

	// KeyFiles is a string that points to file of service account keys
	KeyFiles string

	// SeverityThreshold is the level of security vulns to search for.
	SeverityThreshold int

	// JSONLogSummary signals to the promoter if it should print a JSON summary of the operation
	JSONLogSummary bool

	// OutputFormat is the format we will use for snapshots json/yaml
	OutputFormat string

	// MinimalSnapshot is used in snapshots. but im not sure
	MinimalSnapshot bool

	// SnapshotTag when set, only images with this tag will be snapshotted
	SnapshotTag string

	// Repository container repository to be parsed queried
	Repository string

	// CheckManifestLists this should be a subcommand:
	// (only works with --repository) read snapshot from file and checks all
	// manifest lists have children from the same location
	CheckManifestLists string
}

var DefaultOptions = &Options{}

func (o *Options) Validate() error {
	// If one of the snapshot options is set, manifests will not be checked
	if o.Snapshot == "" && o.ManifestBasedSnapshotOf == "" {
		if o.Manifest == "" && o.ThinManifestDir == "" {
			return errors.New("at least a manifest file or thin manifest directory have to be specified")
		}
	}
	return nil
}

// RunOptions capture the options of a run
type RunOptions struct {
	// Confirm
	Confirm bool

	// Use a service account when true
	UseServiceAcct bool
}
