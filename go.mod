module sigs.k8s.io/k8s-container-image-promoter

go 1.15

require (
	cloud.google.com/go v0.72.0
	cloud.google.com/go/logging v1.1.0
	github.com/cenkalti/backoff/v4 v4.1.0
	github.com/google/go-containerregistry v0.1.3
	github.com/google/uuid v1.1.2
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.1.1
	github.com/stretchr/testify v1.7.0
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1
	google.golang.org/api v0.35.0
	google.golang.org/genproto v0.0.0-20201110150050-8816d57aaa9a
	gopkg.in/src-d/go-git.v4 v4.13.1
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/release v0.6.1-0.20201130132450-1c35c3fb1498
	sigs.k8s.io/release-utils v0.2.1
)
