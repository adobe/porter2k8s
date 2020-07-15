package porter2k8s

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	catalogv1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	contourv1beta1 "github.com/projectcontour/contour/apis/contour/v1beta1"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestProcessObjects(t *testing.T) {
	testCases := []struct {
		file       []string
		gvk        schema.GroupVersionKind
		kubeObject string // Empty if deprecated version
	}{
		{
			[]string{"deprecated_hpa"},
			schema.GroupVersionKind{Group: "autoscaling", Version: "v2beta1", Kind: "HorizontalPodAutoscaler"},
			"",
		},
		{
			[]string{"deprecated_vs"},
			schema.GroupVersionKind{Group: "networking.istio.io", Version: "v1alpha3", Kind: "VirtualService"},
			"",
		},
		{
			[]string{"current_hpa"},
			schema.GroupVersionKind{Group: "autoscaling", Version: "v2beta2", Kind: "HorizontalPodAutoscaler"},
			"HPAutoscaler",
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			var kubeObjects KubeObjects
			kubeObjects.ServiceInstances = make(map[string][]*catalogv1beta1.ServiceInstance)
			kubeObjects.ServiceBindings = make(map[string][]*catalogv1beta1.ServiceBinding)
			kubeObjects.IngressRouteList = new(contourv1beta1.IngressRouteList)

			pwd, _ := os.Getwd()
			filePath := fmt.Sprintf("%s/test", pwd)
			cfg := CmdConfig{ConfigPath: filePath}
			var buf bytes.Buffer
			log.SetOutput(&buf)
			kubeObjects.processObjects(&cfg, tc.file)
			if tc.kubeObject == "" {
				// Validate warning message if deprecated.
				expected := fmt.Sprintf(
					"Object type %s is unsupported, please upgrade to current version to change existing resource",
					tc.gvk.String(),
				)
				if !strings.Contains(buf.String(), expected) {
					t.Errorf("Unexpected message %s\nExpected: %s", buf.String(), expected)
				}
			} else {
				// Validate Object if not deprecated.
				f := reflect.ValueOf(&kubeObjects).Elem().FieldByName(tc.kubeObject)
				if !f.IsValid() || f.IsZero() {
					t.Errorf("KubeObject not populated with object %s", tc.gvk)
				}
			}
		})
	}
}

func captureOutput(f func()) string {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	f()
	log.SetOutput(os.Stderr)
	return buf.String()
}
