module git.corp.adobe.com/EchoSign/porter2k8s

go 1.14

require (
	github.com/frankban/quicktest v1.8.1 // indirect
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/go-test/deep v1.0.5 // indirect
	github.com/golang/protobuf v1.3.4 // indirect
	github.com/google/go-cmp v0.4.0
	github.com/hashicorp/vault/api v1.0.5-0.20200215224050-f6547fa8e820
	github.com/hashicorp/vault/sdk v0.1.14-0.20200305172021-03a3749f220d // indirect
	github.com/kubernetes-sigs/service-catalog v0.3.0-beta.2
	github.com/pierrec/lz4 v2.2.6+incompatible // indirect
	github.com/projectcontour/contour v1.1.0-2.5.0-adobe
	github.com/sirupsen/logrus v1.4.2
	golang.org/x/sys v0.0.0-20200122134326-e047566fdf82 // indirect
	google.golang.org/grpc v1.27.1 // indirect
	gopkg.in/yaml.v2 v2.2.8
	istio.io/api v0.0.0-20200323195549-6bfc9cb1f41e
	istio.io/client-go v0.0.0-20200323200246-27d89de30cc3
	k8s.io/api v0.17.2
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v0.17.2
	sigs.k8s.io/controller-runtime v0.5.1 // indirect
)

replace github.com/projectcontour/contour => github.com/phylake/contour v1.1.0-2.6.1-adobe
