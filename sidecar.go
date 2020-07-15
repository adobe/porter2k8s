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

	log "github.com/sirupsen/logrus"
)

// Determine if the logging sidecar needs to be injected, and if so, append it to the deployment
// The behavior is controlled by the ConfigMap `porter2k8s` defined in the namespace.  Returns
// `false` unless the k/v pair of LOGGING_SIDECAR="true" is set.
func loggingSidecarInjectionRequired(clusterSettings map[string]string) bool {
	if clusterSettings["LOGGING_SIDECAR"] == "true" {
		return true
	}
	return false
}

// Takes a service config that includes both the service deployment and a pod configured to run the logging container.
// The fluent-bit container from the pod is injected into the list of containers for the service.  Additionally, the
// volumes associated with the logging container are moved from the pod to the deployment.  Returns `true` if the two
// append operations complete successfully.
func (regionEnv *RegionEnv) addLoggingSidecarContainer(kubeServiceConfig KubeObjects) bool {
	log.Infof("Attempting to inject logging sidecar container and volumes to %s", kubeServiceConfig.Deployment.Name)
	// Find the fluent-bit container in the sidecar deployment file and append it to the list of containers in the
	// service deployment object
	numContainers := len(kubeServiceConfig.Deployment.Spec.Template.Spec.Containers)
	for _, container := range kubeServiceConfig.LoggingPod.Spec.Containers {
		if container.Name == "fluent-bit" {
			log.Infof("Found container %s", container.Name)
			kubeServiceConfig.Deployment.Spec.Template.Spec.Containers = append(
				kubeServiceConfig.Deployment.Spec.Template.Spec.Containers,
				container,
			)
		}
	}
	// Sanity check to verify that the number of containers in the deployment increased
	if len(kubeServiceConfig.Deployment.Spec.Template.Spec.Containers) != numContainers+1 {
		log.Errorf("Logging sidecar container was not added to %s", kubeServiceConfig.Deployment.Name)
		return false
	}
	log.Infof("Logging sidecar container was added to %s", kubeServiceConfig.Deployment.Name)

	// Append all the associated logging sidecar volumes to the service deployment object
	numVolumes := len(kubeServiceConfig.Deployment.Spec.Template.Spec.Volumes)
	for _, volume := range kubeServiceConfig.LoggingPod.Spec.Volumes {
		log.Infof("Adding volume %s", volume.Name)
		// Override the ConfigMap volume source so that it matches what will get created in func
		// createLoggingSidecarConfigMap
		if volume.Name == "fluent-bit-config" {
			volume.VolumeSource.ConfigMap.Name = fmt.Sprintf("%s-fluent-bit-config", kubeServiceConfig.Deployment.Name)
		}
		kubeServiceConfig.Deployment.Spec.Template.Spec.Volumes = append(
			kubeServiceConfig.Deployment.Spec.Template.Spec.Volumes,
			volume,
		)
	}
	// Sanity check to verify that the number of volumes in the deployment changed
	if len(kubeServiceConfig.Deployment.Spec.Template.Spec.Volumes) == numVolumes {
		log.Errorf("Logging sidecar volumes were not added to %s", kubeServiceConfig.Deployment.Name)
		return false
	}
	log.Infof("Logging sidecar volumes were added to %s", kubeServiceConfig.Deployment.Name)
	return true
}
