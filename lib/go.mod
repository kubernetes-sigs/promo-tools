module sigs.k8s.io/k8s-container-image-promoter/lib

go 1.15

require (
	cloud.google.com/go v0.50.0
	cloud.google.com/go/logging v1.0.0
	github.com/google/go-containerregistry v0.0.0-20200219182403-4336215636f7
	golang.org/x/xerrors v0.0.0-20191011141410-1b5146add898
	google.golang.org/api v0.15.0
	google.golang.org/genproto v0.0.0-20191230161307-f3c370f40bfb
	gopkg.in/src-d/go-git.v4 v4.13.1
	gopkg.in/yaml.v2 v2.2.8
	k8s.io/apimachinery v0.17.3
	k8s.io/klog v1.0.0
	sigs.k8s.io/k8s-container-image-promoter/pkg v0.0.0-00010101000000-000000000000
)

replace sigs.k8s.io/k8s-container-image-promoter/pkg => ../pkg
