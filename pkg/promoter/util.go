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

package promoter

import (
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/promo-tools/v3/internal/version"
)

const vulnerabilityDiscalimer = `DISCLAIMER: Vulnerabilities are found as issues with package
binaries within image layers, not necessarily with the image layers themselves.
So a 'fixable' vulnerability may not necessarily be immediately actionable. For
example, even though a fixed version of the binary is available, it doesn't
necessarily mean that a new version of the image layer is available.`

func printVersion() {
	logrus.Info(version.Get())
}

// printSection handles the start/finish labels in the
// former legacy cli/run code
func printSection(message string, confirm bool) {
	dryRunLabel := ""
	if !confirm {
		dryRunLabel = "(DRY RUN) "
	}
	logrus.Infof("********** %s %s**********", message, dryRunLabel)
}
