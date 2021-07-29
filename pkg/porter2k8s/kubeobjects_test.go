package porter2k8s

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestProcessObjects(t *testing.T) {
	testCases := []struct {
		file       []string
		gvk        schema.GroupVersionKind
		kubeObject string // Empty if deprecated version
		err        string
	}{
		{
			[]string{"deprecated_hpa"},
			schema.GroupVersionKind{Group: "autoscaling", Version: "v2beta1", Kind: "HorizontalPodAutoscaler"},
			"",
			"Object type autoscaling/v2beta1, Kind=HorizontalPodAutoscaler is unsupported",
		},
		{
			[]string{"deprecated_vs"},
			schema.GroupVersionKind{Group: "networking.istio.io", Version: "v1alpha3", Kind: "VirtualService"},
			"",
			"Object type networking.istio.io/v1alpha3, Kind=VirtualService is unsupported",
		},
		{
			[]string{"current_hpa"},
			schema.GroupVersionKind{Group: "autoscaling", Version: "v2beta2", Kind: "HorizontalPodAutoscaler"},
			"HPAutoscaler",
			"",
		},
		{
			[]string{"multiple_pod"},
			schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			"",
			"One and only one pod containing object is allowed. Found 2",
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			var kubeObjects KubeObjects
			kubeObjects.Unstructured = make(map[string][]*unstructured.Unstructured)

			pwd, _ := os.Getwd()
			filePath := fmt.Sprintf("%s/../../test", pwd)
			cfg := CmdConfig{ConfigPath: filePath}
			err := kubeObjects.processObjects(&cfg, tc.file)
			if err != nil {
				if !strings.Contains(err.Error(), tc.err) {
					t.Errorf("Unexpected message %s\nExpected: %s", err, tc.err)
				}
			}
		})
	}
}

// No longer required since processObjects now returns an error.
func captureOutput(f func()) string {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	f()
	log.SetOutput(os.Stderr)
	return buf.String()
}
