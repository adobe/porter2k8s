/*
This file is licensed to you under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License. You may obtain a copy
of the License at http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under
the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR REPRESENTATIONS
OF ANY KIND, either express or implied. See the License for the specific language
governing permissions and limitations under the License.
*/

package porter2k8s

import (
	"fmt"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// logging sidecar Pod test skeleton object
var ethosSidecarPod = v1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name: "fluent-bit",
	},
	Spec: v1.PodSpec{
		Containers: []v1.Container{
			v1.Container{
				Name:  "fluent-bit",
				Image: "fluent/fluent-bit:1.1.1",
			},
		},
		Volumes: []v1.Volume{
			v1.Volume{
				Name: "logging-volume",
			},
			v1.Volume{
				Name: "fluent-data",
			},
			v1.Volume{
				Name: "fluent-bit-config",
				VolumeSource: v1.VolumeSource{
					ConfigMap: &v1.ConfigMapVolumeSource{
						LocalObjectReference: v1.LocalObjectReference{
							Name: "fluent-bit-sidecar-config",
						},
					},
				},
			},
		},
	},
}

/* test gating condition for sidecar injection
test cases are logging_sidecar explicitly set,
and one case where the logging_sidecar key is
missing entirely */
func TestLoggingSidecarInjectionRequired(t *testing.T) {
	testCases := []struct {
		clusterSettings map[string]string
		expected        bool
	}{
		{map[string]string{"LOGGING_SIDECAR": "true"}, true},
		{map[string]string{"LOGGING_SIDECAR": "false"}, false},
		{map[string]string{"REGION": "va6"}, false},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Logging sidecar setting: %+v", tc), func(t *testing.T) {
			result := loggingSidecarInjectionRequired(tc.clusterSettings)
			if result != tc.expected {
				t.Errorf("Test Failed: Unexpected result %t, expected %t", result, tc.expected)
			}
		})
	}
}

/* test adding container and volumes from sidecar pod to deployment
aside from the conditional gating, the sidecar will also fail to
deploy if it isn't explicitly named 'fluent-bit', which is the
only supported logging client currently. */
func TestAddLoggingSidecarContainer(t *testing.T) {
	testCases := []struct {
		sidecarContainerName string
		expected             bool
	}{
		{"fluent-bit", true},
		{"bluent-fit", false},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Initial deployment with logging sidecar: %s", tc.sidecarContainerName), func(t *testing.T) {
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{},
			}
			var kubeServiceConfig KubeObjects
			kubeServiceConfig.Deployment = deployment.DeepCopy()
			kubeServiceConfig.LoggingPod = ethosSidecarPod.DeepCopy()
			kubeServiceConfig.LoggingPod.Spec.Containers[0].Name = tc.sidecarContainerName
			result := regionEnv.addLoggingSidecarContainer(kubeServiceConfig)
			if result != tc.expected {
				t.Errorf("Incorrect behavior detected for sidecar container %s", tc.sidecarContainerName)
			}
		})
	}
}
