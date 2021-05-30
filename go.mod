module sigs.k8s.io/k8s-container-image-promoter

go 1.15

require (
	github.com/docker/docker v1.4.2-0.20191219165747-a9416c67da9f // indirect
	github.com/google/uuid v1.2.0
	github.com/opencontainers/image-spec v1.0.2-0.20200206005212-79b036d80240 // indirect
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.1.3
	github.com/stretchr/testify v1.7.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/release v0.8.1-0.20210529062825-c568a0e710da
	sigs.k8s.io/release-utils v0.2.1
)

replace k8s.io/release => github.com/justaugustus/release v0.0.0-20210530045139-1c92cda49659
