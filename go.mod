module sigs.k8s.io/k8s-container-image-promoter

go 1.15

require (
	github.com/google/uuid v1.1.1
	gopkg.in/yaml.v2 v2.3.0
	k8s.io/klog v1.0.0
	sigs.k8s.io/k8s-container-image-promoter/pkg v0.0.0
)

replace sigs.k8s.io/k8s-container-image-promoter/pkg => ./pkg
