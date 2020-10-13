module sigs.k8s.io/k8s-container-image-promoter

go 1.15

require (
	cloud.google.com/go v0.50.0
	cloud.google.com/go/storage v1.5.0
	github.com/google/uuid v1.1.1
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553
	golang.org/x/xerrors v0.0.0-20191011141410-1b5146add898
	google.golang.org/api v0.15.0
	google.golang.org/genproto v0.0.0-20191230161307-f3c370f40bfb
	gopkg.in/yaml.v2 v2.2.8
	k8s.io/klog v1.0.0
	sigs.k8s.io/k8s-container-image-promoter/pkg v0.0.0
	sigs.k8s.io/yaml v1.2.0
)

replace (
	sigs.k8s.io/k8s-container-image-promoter/pkg => ./pkg
)
