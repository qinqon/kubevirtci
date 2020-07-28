module kubevirt.io/kubevirtci/tools/releaser

go 1.13

require (
	github.com/google/go-containerregistry v0.0.0-20200115214256-379933c9c22b
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.6.0
	golang.org/x/time v0.0.0-20200630173020-3af7569d3a1e // indirect
	k8s.io/apimachinery v0.17.3
	k8s.io/client-go v9.0.0+incompatible
	k8s.io/test-infra v0.0.0-20200728085909-4407d8aec1ee
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v12.2.0+incompatible
	k8s.io/client-go => k8s.io/client-go v0.17.3
)
