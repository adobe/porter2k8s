/*
Copyright 2018 Adobe. All rights reserved.
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
	"os"
	"regexp"

	"git.corp.adobe.com/EchoSign/porter2k8s/pkg/vault"

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// KubeObjects is the global configuration for the service.
type KubeObjects struct {
	ClusterConfigs ClusterConfigs
	PodObject      *unstructured.Unstructured
	Unstructured   map[string][]*unstructured.Unstructured
}

// getKubeConfigs fetches Kube Configs from vault.
func (kubeObjects *KubeObjects) getKubeConfigs(cfg *CmdConfig) error {
	clusterListFileName := fmt.Sprintf("%s/%s", cfg.ConfigPath, clusterConfigFile)
	if _, err := os.Stat(clusterListFileName); err == nil {
		log.Infof("Retrieving kubeconfig from Vault using %s", clusterListFileName)
		client, err := vault.NewVaultClient(cfg.VaultAddress, cfg.VaultNamespace, cfg.VaultToken)
		if err != nil {
			return fmt.Errorf("vault client initialization error %s\n%s", cfg.ConfigType, err)
		}
		err = kubeObjects.KubeConfigFromVault(client, clusterListFileName)
		if err != nil {
			return fmt.Errorf("unable to pull cluster configuration from Vault %s", err)
		}
	}
	return nil
}

// KubeConfigFromVault reads a cluster config file, pulls from Vault and instantiates k8s client cluster configurations.
func (kubeObjects *KubeObjects) KubeConfigFromVault(vaultClient vault.VaultClientInterface, fileName string) error {
	clusters, err := readReferences(fileName)
	if err != nil {
		return err
	}
	err = clusters.fetchConfigs(vaultClient)
	if err != nil {
		return err
	}
	kubeObjects.ClusterConfigs = clusters
	return nil
}

// Read objects from yaml files and populate kubeObjects.
func (kubeObjects *KubeObjects) processObjects(cfg *CmdConfig, possibleFiles []string) error {
	var podObjects []*unstructured.Unstructured
	// Determine if kubernetes yaml files are in the config directory or the k8s subdirectory
	objectDirectory := fmt.Sprintf("%s/k8s", cfg.ConfigPath)
	if _, err := os.Stat(objectDirectory); os.IsNotExist(err) {
		objectDirectory = cfg.ConfigPath
	}
	log.Infof("Using %s as directory for Kubernetes yaml objects.", objectDirectory)

	for _, fileType := range possibleFiles {
		var foundFileName string
		fileNameEnv := fmt.Sprintf("%s/%s-%s.yaml", objectDirectory, fileType, cfg.Environment)
		fileNameGeneral := fmt.Sprintf("%s/%s.yaml", objectDirectory, fileType)
		for _, fileName := range []string{fileNameEnv, fileNameGeneral} {
			if _, err := os.Stat(fileName); os.IsNotExist(err) {
				log.Infof("%s not found.", fileName)
			} else {
				log.Infof("Using %s for %s.", fileName, fileType)
				foundFileName = fileName
				break
			}
		}
		// If no file name was found, try the next possible name.
		if foundFileName == "" {
			continue
		}
		objects, readErr := multiDecoder(foundFileName)
		if readErr != nil {
			return readErr
		}
		for _, decoded := range objects {
			object := decoded.Object
			unstructuredObject, err := serviceFromObject(object, nil)
			if err != nil {
				return err
			}
			switch unstructuredObject.GetAPIVersion() {
			case "apps/v1", "batch/v1":
				podObjects = append(podObjects, unstructuredObject)
			case "v1":
				if unstructuredObject.GetKind() == "Pod" {
					log.Warnf("Pods are not supported outside of deployments, jobs, etc.. %s will not be deployed",
						unstructuredObject.GetName())
				} else {
					kubeObjects.Unstructured["all"] = append(kubeObjects.Unstructured["all"], unstructuredObject)
				}
			case "autoscaling/v2beta2", "contour.heptio.com/v1beta1", "monitoring.coreos.com/v1",
				"networking.istio.io/v1beta1", "policy/v1beta1":
				kubeObjects.Unstructured["all"] = append(kubeObjects.Unstructured["all"], unstructuredObject)
			case "dynamodb.services.k8s.aws/v1alpha1", "elasticache.services.k8s.aws/v1alpha1":
				kubeObjects.Unstructured["aws"] = append(kubeObjects.Unstructured["aws"], unstructuredObject)
			case "azure.microsoft.com/v1alpha1", "azure.microsoft.com/v1alpha2", "azure.microsoft.com/v1beta1":
				kubeObjects.Unstructured["azure"] = append(kubeObjects.Unstructured["azure"], unstructuredObject)
			case "servicecatalog.k8s.io/v1beta1":
				if fileType == "servicecatalog-aws" {
					kubeObjects.Unstructured["aws"] = append(kubeObjects.Unstructured["aws"], unstructuredObject)
				} else if fileType == "servicecatalog-azure" {
					kubeObjects.Unstructured["azure"] = append(kubeObjects.Unstructured["aws"], unstructuredObject)
				} else {
					log.Warnf(
						"Excluding Service Instance found in %s, not an AWS or Azure specific servicecatalog file",
						foundFileName,
					)
				}
			default:
				return fmt.Errorf(
					"Object type %s is unsupported, please upgrade to current version",
					decoded.GVK,
				)
			}
		}
	}

	if len(podObjects) != 1 {
		return fmt.Errorf("One and only one pod containing object is allowed. Found %d", len(podObjects))
	}
	kubeObjects.PodObject = podObjects[0]

	return nil
}

// validate ensure that all kubernetes resources contain the service name.
// This prevents different microservices from colliding.
// It also adds the name to the tags of each resource.
func (kubeObjects *KubeObjects) validateAndTag() error {

	// Additional Deployment checks are handled in prepareDeployment.
	// Order of preference: pod object annotation, name of service object, name of pod object.
	var serviceName string
	services := kubeObjects.findUnstructured("v1", "Service", "all")
	podObjectName := kubeObjects.PodObject.GetName()
	podObjectAnnotations := kubeObjects.PodObject.GetAnnotations()
	log.Infof("Annotations %+v", podObjectAnnotations)
	serviceNameOverride, ok := podObjectAnnotations["porter2k8s/service-name"]
	if ok {
		serviceName = serviceNameOverride
	} else if len(services) > 0 {
		serviceName = services[0].GetName()
	} else {
		serviceName = podObjectName
	}
	log.Infof("serviceName %s", serviceName)

	for _, cloud := range []string{"aws", "azure", "all"} {
		for _, object := range kubeObjects.Unstructured[cloud] {
			err := validateUnstructured(object, serviceName, podObjectName)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// findUnstructured returns all unstructured objects of matching an API Version and Kind, or just the APIVersion if
// the Kind is empty.
// key parameter is "all", "aws", or "azure"
func (kubeObjects *KubeObjects) findUnstructured(apiVersion, kind string, key string) []*unstructured.Unstructured {
	var output []*unstructured.Unstructured
	for _, service := range kubeObjects.Unstructured[key] {
		if kind == "" {
			if service.GetAPIVersion() == apiVersion {
				log.Infof("Found %s", apiVersion)
				output = append(output, service)
			}
		} else {
			if service.GetAPIVersion() == apiVersion && service.GetKind() == kind {
				log.Infof("Found %s %s", apiVersion, kind)
				output = append(output, service)
			}
		}
	}
	if len(output) == 0 {
		log.Infof("Found no %s %s", apiVersion, kind)
	}

	return output
}

// prepareDeployment takes the deployment from the yaml file and applies image sha, labels, and configmap and secret
// references.
func (kubeObjects *KubeObjects) prepareDeployment(sha string) error {

	// Add sha to image, configMapRef, and secretRef
	// Find container in deployment that matches the deployment name
	applicationContainer, typedObject := findContainer(kubeObjects.PodObject)
	if applicationContainer == nil {
		return fmt.Errorf("unable to find application image in deployment spec")
	}

	// Remove docker tag, if it exists.
	// The image in the deployment.yaml should not have a tag.
	shaReplacement := fmt.Sprintf("$1:%s", sha)
	// Replace everything after the colon, if it exists. See tests for examples.
	regex := regexp.MustCompile(`(.*?)(:|\z).*`)
	applicationContainer.Image = regex.ReplaceAllString(applicationContainer.Image, shaReplacement)

	refName := fmt.Sprintf("%s-%s", kubeObjects.PodObject.GetName(), sha)
	// Stub out references to configmap and secret ref to be filled out for each region later.
	envSourceConfigMap := v1.EnvFromSource{
		ConfigMapRef: &v1.ConfigMapEnvSource{
			v1.LocalObjectReference{
				Name: refName,
			},
			boolRef(false),
		},
	}
	envSourceSecret := v1.EnvFromSource{
		SecretRef: &v1.SecretEnvSource{
			v1.LocalObjectReference{
				Name: refName,
			},
			// Secrets may not be defined.
			boolRef(true),
		},
	}
	applicationContainer.EnvFrom = append(applicationContainer.EnvFrom, envSourceConfigMap, envSourceSecret)

	// Add sha label to deployment and pod.
	appendLabel(typedObject, "sha", sha)
	appendLabel(findPodTemplateSpec(typedObject), "sha", sha)

	// Service Side Apply Bug fixed in 1.20. Need to add protocol to ports in the meantime.
	// https://github.com/kubernetes-sigs/structured-merge-diff/issues/130
	for i, port := range applicationContainer.Ports {
		if port.Protocol == "" {
			applicationContainer.Ports[i].Protocol = v1.ProtocolTCP
		}
	}

	// Back to unstructured
	newUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(typedObject)
	if err != nil {
		return err
	}
	kubeObjects.PodObject.Object = newUnstructured

	// Delete replicas if HPA exists.
	hpas := kubeObjects.findUnstructured("autoscaling/v2beta2", "HorizontalPodAutoscaler", "all")
	if len(hpas) > 0 {
		unstructured.RemoveNestedField(kubeObjects.PodObject.Object, "spec", "replicas")
	}

	return nil
}
