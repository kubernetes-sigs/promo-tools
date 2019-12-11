module sigs.k8s.io/k8s-container-image-promoter

go 1.13

require (
	cloud.google.com/go/pubsub v1.0.1
	cloud.google.com/go/storage v1.1.2
	github.com/google/go-containerregistry v0.0.0-20191023194145-7683b4ee5f61
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	google.golang.org/api v0.11.0
	gopkg.in/src-d/go-git.v4 v4.11.0
	gopkg.in/yaml.v2 v2.2.4
	k8s.io/apimachinery v0.0.0-20191024025529-62ce3d1e6a82
	k8s.io/klog v1.0.0
	sigs.k8s.io/yaml v1.1.0
)

replace (
	github.com/Sirupsen/logrus v1.0.5 => github.com/sirupsen/logrus v1.4.2
	github.com/Sirupsen/logrus v1.0.6 => github.com/sirupsen/logrus v1.4.2
	github.com/Sirupsen/logrus v1.1.1 => github.com/sirupsen/logrus v1.4.2
	github.com/Sirupsen/logrus v1.2.0 => github.com/sirupsen/logrus v1.4.2
	github.com/Sirupsen/logrus v1.3.0 => github.com/sirupsen/logrus v1.4.2
	github.com/Sirupsen/logrus v1.4.0 => github.com/sirupsen/logrus v1.4.2
	github.com/Sirupsen/logrus v1.4.1 => github.com/sirupsen/logrus v1.4.2
	github.com/Sirupsen/logrus v1.4.2 => github.com/sirupsen/logrus v1.4.2
)
