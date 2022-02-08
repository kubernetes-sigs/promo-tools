package promoter

import (
	"github.com/pkg/errors"
	reg "sigs.k8s.io/promo-tools/v3/legacy/dockerregistry"
)

// ScanEdges runs the vulnerability scans on the new images
// detected by the promoter.
func (di *defaultPromoterImplementation) ScanEdges(
	opts *Options, sc *reg.SyncContext,
	promotionEdges map[reg.PromotionEdge]interface{},
) error {
	if err := sc.RunChecks(
		[]reg.PreCheck{
			reg.MKImageVulnCheck(
				sc,
				promotionEdges,
				opts.SeverityThreshold,
				nil,
			),
		},
	); err != nil {
		return errors.Wrap(err, "checking image vulnerabilities")
	}
	return nil
}
