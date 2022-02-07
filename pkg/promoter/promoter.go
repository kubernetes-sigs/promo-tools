package promoter

var AllowedOutputFormats = []string{
	"csv",
	"yaml",
}

type Promoter struct {
	Options *Options
	impl    promoterImplementation
}

func New() *Promoter {
	return &Promoter{
		Options: DefaultOptions,
		impl:    defaultPromoterImplementation{},
	}
}

// promoterImplementation
type promoterImplementation interface{}

func (p *Promoter) PromoteImages(opts *Options) (err error) {
	// STUB
	return nil
}

func (p *Promoter) Snapshot(opts *Options) error {
	// STUB
	return nil
}

func (p *Promoter) SecurityScan(opts *Options) error {
	// STUB
	return nil
}

func (p *Promoter) CheckManifestLists(opts *Options) error {
	// STUB
	return nil
}

type defaultPromoterImplementation struct{}
