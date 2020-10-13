module sigs.k8s.io/k8s-container-image-promoter

go 1.15

require (
	cloud.google.com/go v0.64.0
	cloud.google.com/go/storage v1.10.0
	github.com/google/uuid v1.1.1
	golang.org/x/net v0.0.0-20200822124328-c89045814202
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1
	google.golang.org/api v0.30.0
	google.golang.org/genproto v0.0.0-20200827165113-ac2560b5e952
	gopkg.in/yaml.v2 v2.3.0
	k8s.io/klog v1.0.0
	sigs.k8s.io/k8s-container-image-promoter/pkg v0.0.0
	sigs.k8s.io/yaml v1.2.0
)

replace sigs.k8s.io/k8s-container-image-promoter/pkg => ./pkg
