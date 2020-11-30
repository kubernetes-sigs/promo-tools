/*
Copyright 2020 The Kubernetes Authors.

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

package cmd

import (
	"os"

	guuid "github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"k8s.io/release/pkg/cip/audit"
)

// auditCmd represents the base command when called without any subcommands
// TODO: Update command description.
var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Run the image auditor",
	Long: `cip audit - Image auditor

Start an audit server that responds to Pub/Sub push events.
`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return errors.Wrap(
			runImageAuditor(auditOpts),
			"run `cip audit`",
		)
	},
}

// TODO: Push these into a package.
type auditOptions struct {
	projectID    string
	repoURL      string
	repoBranch   string
	manifestPath string
	uuid         string
}

var auditOpts = &auditOptions{}

func init() {
	auditCmd.PersistentFlags().StringVar(
		&auditOpts.projectID,
		"project",
		"",
		"GCP project name (used for labeling error reporting logs in GCP)",
	)

	auditCmd.PersistentFlags().StringVar(
		&auditOpts.repoURL,
		"url",
		"",
		"repository URL for promoter manifests",
	)

	auditCmd.PersistentFlags().StringVar(
		&auditOpts.repoBranch,
		"branch",
		"",
		"git branch of the promoter manifest repo to checkout",
	)

	auditCmd.PersistentFlags().StringVar(
		&auditOpts.manifestPath,
		"path",
		"",
		"manifest path (relative to the root of promoter manifest repo)",
	)

	rootCmd.AddCommand(auditCmd)
}

func runImageAuditor(opts *auditOptions) error {
	opts.set()

	if err := validateAuditOptions(opts); err != nil {
		return errors.Wrap(err, "validating audit options")
	}

	auditorContext, err := audit.InitRealServerContext(
		opts.projectID,
		opts.repoURL,
		opts.repoBranch,
		opts.manifestPath,
		opts.uuid,
	)
	if err != nil {
		return errors.Wrap(err, "creating auditor context")
	}

	auditorContext.RunAuditor()

	return nil
}

func (o *auditOptions) set() {
	logrus.Infof("Setting image auditor options...")

	if o.projectID == "" {
		o.projectID = os.Getenv("CIP_AUDIT_GCP_PROJECT_ID")
	}

	if o.repoURL == "" {
		o.repoURL = os.Getenv("CIP_AUDIT_MANIFEST_REPO_URL")
	}

	if o.repoBranch == "" {
		o.repoBranch = os.Getenv("CIP_AUDIT_MANIFEST_REPO_BRANCH")
	}

	if o.manifestPath == "" {
		o.manifestPath = os.Getenv("CIP_AUDIT_MANIFEST_REPO_MANIFEST_DIR")
	}

	// TODO: Should we allow this to be configurable via the command line?
	o.uuid = os.Getenv("CIP_AUDIT_TESTCASE_UUID")
	if len(o.uuid) > 0 {
		logrus.Infof("Starting auditor in Test Mode (%s)", o.uuid)
	} else {
		o.uuid = guuid.New().String()
		logrus.Infof("Starting auditor in Regular Mode (%s)", o.uuid)
	}

	logrus.Infof(
		// nolint: lll
		"Image auditor options: [GCP project: %s, repo URL: %s, repo branch: %s, path: %s, UUID: %s]",
		o.repoURL,
		o.repoBranch,
		o.manifestPath,
		o.projectID,
		o.uuid,
	)
}

func validateAuditOptions(o *auditOptions) error {
	// TODO: Validate root options
	// TODO: Validate audit options
	return nil
}
