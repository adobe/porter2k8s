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
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/restmapper"
)

var logger *log.Entry = log.WithFields(log.Fields{"Region": "test"})

var resources []*restmapper.APIGroupResources = []*restmapper.APIGroupResources{
	{
		Group: metav1.APIGroup{
			Versions: []metav1.GroupVersionForDiscovery{
				{Version: "v1"},
			},
			PreferredVersion: metav1.GroupVersionForDiscovery{Version: "v1"},
		},
		VersionedResources: map[string][]metav1.APIResource{
			"v1": {
				{Name: "configmaps", Namespaced: true, Kind: "ConfigMap"},
				{Name: "pods", Namespaced: true, Kind: "Pod"},
				{Name: "secrets", Namespaced: true, Kind: "Secret"},
			},
		},
	},
}

var configMapList = unstructured.UnstructuredList{
	Items: []unstructured.Unstructured{
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name": "testapp-0123456",
					"labels": map[string]interface{}{
						"app": "testapp",
						"sha": "0123456",
					},
					"creationTimestamp": "2001-01-01T00:00:00Z",
				},
			},
		},
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name": "testapp-1234567",
					"labels": map[string]interface{}{
						"app": "testapp",
						"sha": "1234567",
					},
					"creationTimestamp": "2002-01-01T00:00:00Z",
				},
			},
		},
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name": "testapp-2345678",
					"labels": map[string]interface{}{
						"app": "testapp",
						"sha": "2345678",
					},
					"creationTimestamp": "2003-01-01T00:00:00Z",
				},
			},
		},
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name": "testapp-3456789",
					"labels": map[string]interface{}{
						"app": "testapp",
						"sha": "3456789",
					},
					"creationTimestamp": "2004-01-01T00:00:00Z",
				},
			},
		},
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name": "testapp-fluent-bit-config",
					"labels": map[string]interface{}{
						"app": "testapp",
					},
					"creationTimestamp": "2004-01-01T00:00:00Z",
				},
			},
		},
	},
}

var secretList = unstructured.UnstructuredList{
	Items: []unstructured.Unstructured{
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata": map[string]interface{}{
					"name": "testapp-0123456",
					"labels": map[string]interface{}{
						"app": "testapp",
						"sha": "0123456",
					},
					"creationTimestamp": "2001-01-01T00:00:00Z",
				},
			},
		},
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata": map[string]interface{}{
					"name": "testapp-1234567",
					"labels": map[string]interface{}{
						"app": "testapp",
						"sha": "1234567",
					},
					"creationTimestamp": "2002-01-01T00:00:00Z",
				},
			},
		},
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata": map[string]interface{}{
					"name": "testapp-2345678",
					"labels": map[string]interface{}{
						"app": "testapp",
						"sha": "2345678",
					},
					"creationTimestamp": "2003-01-01T00:00:00Z",
				},
			},
		},
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata": map[string]interface{}{
					"name": "testapp-3456789",
					"labels": map[string]interface{}{
						"app": "testapp",
						"sha": "3456789",
					},
					"creationTimestamp": "2004-01-01T00:00:00Z",
				},
			},
		},
		unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Secret",
				"metadata": map[string]interface{}{
					"name": "testapp-secret",
					"labels": map[string]interface{}{
						"app":    "testapp",
						"retain": "true",
					},
					"creationTimestamp": "2005-01-01T00:00:00Z",
				},
			},
		},
	},
}

var namedSecrets = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name": "Service-Named-Secret",
			"labels": map[string]interface{}{
				"app": "testapp",
			},
		},
		"data": map[string]interface{}{
			"mysecret": "{{.SECRET_VALUE}}",
		},
	},
}

var namedSecretsError = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name": "Bad-Example-Secret",
			"labels": map[string]interface{}{
				"app": "testapp",
			},
		},
		"data": map[string]interface{}{
			"mysecret": "U3RhdGljU2VjcmV0Rm9yYmlkZGVuCg==",
		},
	},
}

var deployment = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]interface{}{
			"name": "testapp",
		},
		"spec": map[string]interface{}{
			"replicas": int64(2),
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "testapp",
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": "testapp",
					},
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "testapp",
							"image": "test-registry/test-org/test-image",
							"ports": []interface{}{
								map[string]interface{}{
									"containerPort": int64(8080),
									"name":          "test",
								},
							},
						},
					},
				},
			},
		},
	},
}

var statefulSet = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "StatefulSet",
		"metadata": map[string]interface{}{
			"name": "teststatefulset",
		},
		"spec": map[string]interface{}{
			"replicas": int64(2),
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "testapp",
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": "testapp",
					},
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "teststatefulset",
							"image": "test-registry/test-org/test-image",
						},
					},
				},
			},
			"volumeClaimTemplates": []interface{}{
				map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "testpvc",
					},
					"spec": map[string]interface{}{
						"accessModes": []interface{}{
							"ReadWriteOnce",
						},
						"resources": map[string]interface{}{
							"requests": map[string]interface{}{
								"storage": "1Ti",
							},
						},
					},
				},
			},
		},
	},
}

var nilReplicaDeployment = unstructured.Unstructured{
	Object: map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "nil-replica-testapp",
		},
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "nil-replica-testapp",
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": "nil-replica-testapp",
					},
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "nil-replica-testapp",
							"image": "test-registry/test-org/test-image",
						},
					},
				},
			},
		},
	},
}

var job = unstructured.Unstructured{
	Object: map[string]interface{}{
		"kind":       "Job",
		"apiVersion": "batch/v1",
		"metadata": map[string]interface{}{
			"name": "testjob",
		},
		"spec": map[string]interface{}{
			"backoffLimit":          int64(0),
			"activeDeadlineSeconds": int64(300),
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": "testapp",
					},
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "testapp",
							"image": "test-registry/test-org/test-image",
						},
					},
				},
			},
		},
	},
}

var ingressRouteInternal = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "contour.heptio.com/v1beta1",
		"kind":       "IngressRoute",
		"metadata": map[string]interface{}{
			"name": "testservice-ingressrouteinternal",
			"annotations": map[string]interface{}{
				"kubernetes.io/ingress.class": "contour-internal",
			},
		},
		"spec": map[string]interface{}{
			"virtualhost": map[string]interface{}{
				"fqdn": "test-app.micro.test.com",
				"tls": map[string]interface{}{
					"secretName": "heptio-contour/cluster-ssl-int",
				},
			},
		},
	},
}

var ingressRouteExternal = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "contour.heptio.com/v1beta1",
		"kind":       "IngressRoute",
		"metadata": map[string]interface{}{
			"name": "testservice-ingressrouteexternal",
			"annotations": map[string]interface{}{
				"kubernetes.io/ingress.class": "contour-public",
			},
		},
		"spec": map[string]interface{}{
			"virtualhost": map[string]interface{}{
				"fqdn": "other-app.micro.test.com",
				"tls": map[string]interface{}{
					"secretName": "heptio-contour/cluster-ssl-public",
				},
			},
		},
	},
}

var gateway = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "networking.istio.io/v1beta1",
		"kind":       "Gateway",
		"metadata": map[string]interface{}{
			"name": "testgw",
		},
		"spec": map[string]interface{}{
			"servers": []interface{}{
				map[string]interface{}{
					"hosts": []interface{}{"test-app.micro.test.com", "other-app.micro.test.com"},
					"port": map[string]interface{}{
						"number":   int64(80),
						"protocol": "HTTP",
						"name":     "http",
					},
				},
				map[string]interface{}{
					"hosts": []interface{}{"test-app.micro.test.com", "other-app.micro.test.com"},
					"port": map[string]interface{}{
						"number":   int64(443),
						"protocol": "HTTPS",
						"name":     "https",
					},
					"tls": map[string]interface{}{
						"mode":              int64(1),
						"privateKey":        "/etc/istio/ingressgateway-certs/tls.key",
						"serverCertificate": "/etc/istio/ingressgateway-certs/tls.crt",
					},
				},
			},
		},
	},
}

var virtualService = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "networking.istio.io/v1beta1",
		"kind":       "VirtualService",
		"metadata": map[string]interface{}{
			"name": "testvirtualservice",
		},
		"spec": map[string]interface{}{
			"hosts":    []interface{}{"test-app.micro.test.com", "other-app.micro.test.com"},
			"gateways": []interface{}{"testgw"},
			"http": []interface{}{
				map[string]interface{}{
					"route": []interface{}{
						map[string]interface{}{
							"destination": map[string]interface{}{
								"host": "testapp",
								"port": map[string]interface{}{
									"number": int64(80),
								},
							},
							"weight": int64(100),
						},
					},
				},
			},
		},
	},
}

var horizontalPodAutoscaler = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "autoscaling/v2beta2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata": map[string]interface{}{
			"name": "testhorizontalpodautoscaler",
		},
		"spec": map[string]interface{}{
			"scaleTargetRef": map[string]interface{}{
				"kind":       "Deployment",
				"name":       "testapp",
				"apiVersion": "apps/v1",
			},
			"minReplicas": int64(1),
			"maxReplicas": int64(10),
			"metrics": []interface{}{
				map[string]interface{}{
					"resource": map[string]interface{}{
						"name": "cpu",
						"target": map[string]interface{}{
							"averageUtilization": int64(70),
							"type":               "Utilization",
						},
						"type": "Resource",
					},
				},
			},
		},
	},
}

var horizontalPodAutoscalerTokens = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "autoscaling/v2beta2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata": map[string]interface{}{
			"name": "testhorizontalpodautoscalertokens",
		},
		"spec": map[string]interface{}{
			"scaleTargetRef": map[string]interface{}{
				"kind":       "Deployment",
				"name":       "testapp",
				"apiVersion": "apps/v1",
			},
			"minReplicas": int64(1),
			"maxReplicas": int64(10),
			"metrics": []interface{}{
				map[string]interface{}{
					"type": "External",
					"external": map[string]interface{}{
						"metric": map[string]interface{}{
							"name": "usm_external_exporter_queue_length",
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"queue":  "{{.QUEUE_NAME}}",
									"static": "this-should-never-change",
								},
							},
						},
						"target": map[string]interface{}{
							"averageValue": int64(15),
							"type":         "AverageValue",
						},
						"type": "Resource",
					},
				},
			},
		},
	},
}

var serviceBinding = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "servicecatalog.k8s.io/v1beta1",
		"kind":       "ServiceBinding",
		"metadata": map[string]interface{}{
			"name": "testservicebinding",
		},
		"spec": map[string]interface{}{
			"secretName": "test-secret",
			"instanceRef": map[string]interface{}{
				"name": "test-database",
			},
		},
	},
}

var serviceInstance1 = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "servicecatalog.k8s.io/v1beta1",
		"kind":       "ServiceInstance",
		"metadata": map[string]interface{}{
			"name": "testserviceinstance",
			"labels": map[string]interface{}{
				"irrelevant": "value",
			},
		},
		"spec": map[string]interface{}{
			"clusterServiceClassRef": map[string]interface{}{
				"name": "98374372-8eac-423c-af25-75af9203451e",
			},
			"clusterServicePlanRef": map[string]interface{}{
				"name": "9af208c8-b219-1139-9ea4-d6e41afdb762",
			},
			"parameters": map[string]interface{}{
				"subnetID: ": "parameters",
			},
		},
	},
}

var serviceInstance2 = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "servicecatalog.k8s.io/v1beta1",
		"kind":       "ServiceInstance",
		"metadata": map[string]interface{}{
			"name": "testserviceinstance2",
		},
		"spec": map[string]interface{}{
			"clusterServiceClassRef": map[string]interface{}{
				"name": "98374372-8eac-423c-af25-75af9203451e",
			},
			"clusterServicePlanRef": map[string]interface{}{
				"name": "9af208c8-b219-1139-9ea4-d6e41afdb762",
			},
		},
	},
}

func TestCreateConfigMap(t *testing.T) {
	testCases := []struct {
		deploymentSha string
	}{
		{"267c65f"}, // Try to create ConfigMap with sha that does not exist.
		{"0123456"}, // Try to create ConfigMap with sha that does exist.	}
	}
	ctx, cancel := context.WithCancel(context.Background())
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	}
	defer cancel()
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				gvrToListKind, &configMapList)
			mapper := restmapper.NewDiscoveryRESTMapper(resources)

			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					SHA: tc.deploymentSha,
				},
				Context:       ctx,
				DynamicClient: dynamicClient,
				Logger:        logger,
				Mapper:        mapper,
			}
			configMapFound := false
			listOptions := metav1.ListOptions{
				LabelSelector: "app=testapp",
			}
			gvk := schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
			mapping, _ := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace("")

			createSucceeded := regionEnv.createConfigMap("testapp")
			if !createSucceeded {
				t.Errorf("Unexpected Config Map creation failure\n%s", regionEnv.Errors)
			}
			remainingConfigMaps, _ := dynamicInterface.List(ctx, listOptions)
			// Ensure ConfigMap exists
			for _, remainingConfigMap := range remainingConfigMaps.Items {
				if strings.Contains(remainingConfigMap.GetName(), tc.deploymentSha) {
					configMapFound = true
				}
			}
			if !configMapFound {
				t.Errorf("ConfigMap with sha %s not found.\n", tc.deploymentSha)
			}
		})
	}
}

func TestDeleteOldConfigMaps(t *testing.T) {
	testCases := []struct {
		maxConfigMaps            int
		protectedSha             string
		appName                  string
		expectedConfigMapCount   int
		expectedDeletedConfigMap string
	}{ // One configmMap is missing the "sha" label, so it wil not be deleted.
		{4, "", "testapp", 5, ""},                // Nothing deleted.
		{3, "", "testapp", 4, "testapp-0123456"}, // Oldest CM deleted.
		{3, "0123456", "testapp", 5, ""},         // Nothing deleted due to protected SHA.
		{3, "", "otherapp", 5, ""},               // Nothing deleted due to other app name SHA.
	}

	ctx, cancel := context.WithCancel(context.Background())
	listOptions := metav1.ListOptions{
		LabelSelector: "app=testapp",
	}
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	}
	defer cancel()
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				gvrToListKind, &configMapList)
			mapper := restmapper.NewDiscoveryRESTMapper(resources)

			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					MaxConfigMaps: tc.maxConfigMaps,
				},
				Context:       ctx,
				DynamicClient: dynamicClient,
				Logger:        logger,
				Mapper:        mapper,
			}

			gvk := schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
			mapping, _ := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace("")

			protectedShas := map[string]bool{
				tc.protectedSha: true,
			}

			deleteSuccessful := regionEnv.deleteOldEnv(tc.appName, protectedShas, "ConfigMap")
			if !deleteSuccessful {
				t.Error(regionEnv.Errors)
			}
			// Check if the correct number of configmaps are returned
			remainingConfigMaps, _ := dynamicInterface.List(ctx, listOptions)
			remainingConfigMapsCount := len(remainingConfigMaps.Items)
			if remainingConfigMapsCount != tc.expectedConfigMapCount {
				t.Errorf(
					"Incorrect number of ConfigMaps remaining %d, expected %d.\n",
					remainingConfigMapsCount,
					tc.expectedConfigMapCount)
			}
			// Ensure the correct configmap was deleted
			for _, remainingConfigMap := range remainingConfigMaps.Items {
				if tc.expectedDeletedConfigMap == remainingConfigMap.GetName() {
					t.Errorf("Incorrect ConfigMap deleted, %s remains.\n", tc.expectedDeletedConfigMap)
				}
			}
		})
	}
}

func TestCreateSecret(t *testing.T) {
	testCases := []struct {
		deploymentSha string
	}{
		{"267c65f"}, // Try to create Secret with sha that does not exist.
		{"0123456"}, // Try to create Secret with sha that does exist.
	}
	ctx, cancel := context.WithCancel(context.Background())
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "secrets"}: "SecretList",
	}
	defer cancel()
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				gvrToListKind, &secretList)
			mapper := restmapper.NewDiscoveryRESTMapper(resources)
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					SHA: tc.deploymentSha,
				},
				Context:       ctx,
				DynamicClient: dynamicClient,
				Logger:        logger,
				Mapper:        mapper,
			}
			gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
			mapping, _ := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace("")
			secretFound := false
			listOptions := metav1.ListOptions{
				LabelSelector: "app=testapp",
			}

			createSucceeded := regionEnv.createSecret("testapp")
			if !createSucceeded {
				t.Errorf("Unexpected Config Map creation failure\n%s", regionEnv.Errors)
			}
			remainingSecrets, _ := dynamicInterface.List(ctx, listOptions)
			// Ensure Secret exists
			for _, remainingSecret := range remainingSecrets.Items {
				if strings.Contains(remainingSecret.GetName(), tc.deploymentSha) {
					secretFound = true
				}
			}
			if !secretFound {
				t.Errorf("Secret with sha %s not found.\n", tc.deploymentSha)
			}
		})
	}
}

func TestDeleteOldSecrets(t *testing.T) {
	testCases := []struct {
		maxSecrets            int
		protectedSha          string
		appName               string
		expectedSecretCount   int
		expectedDeletedSecret string
	}{
		{4, "", "testapp", 4, ""},                // Nothing deleted.
		{3, "", "testapp", 3, "testapp-0123456"}, // Oldest Secret deleted.
		{3, "0123456", "testapp", 4, ""},         // Nothing deleted due to protected SHA.
		{3, "", "otherapp", 4, ""},               // Nothing deleted due to other app name SHA.
	}

	var err error
	ctx, cancel := context.WithCancel(context.Background())
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "secrets"}: "SecretList",
	}
	listOptions := metav1.ListOptions{
		LabelSelector: "app=testapp,retain!=true",
	}
	defer cancel()
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				gvrToListKind, &secretList)
			mapper := restmapper.NewDiscoveryRESTMapper(resources)
			protectedShas := map[string]bool{
				tc.protectedSha: true,
			}
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					MaxConfigMaps: tc.maxSecrets,
				},
				Context:       ctx,
				DynamicClient: dynamicClient,
				Logger:        logger,
				Mapper:        mapper,
			}
			gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
			mapping, _ := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace("")

			deleteSuccessful := regionEnv.deleteOldEnv(tc.appName, protectedShas, "Secret")
			if !deleteSuccessful {
				t.Error(regionEnv.Errors)
			}
			if err != nil {
				t.Error(err.Error())
			}
			// Check if the correct number of secrets are returned
			remainingSecrets, _ := dynamicInterface.List(ctx, listOptions)
			remainingSecretsCount := len(remainingSecrets.Items)
			if remainingSecretsCount != tc.expectedSecretCount {
				t.Errorf(
					"Incorrect number of Secrets remaining %d, expected %d.\n",
					remainingSecretsCount,
					tc.expectedSecretCount)
			}
			// Ensure the correct secret was deleted
			for _, remainingSecret := range remainingSecrets.Items {
				if tc.expectedDeletedSecret == remainingSecret.GetName() {
					t.Errorf("Incorrect Secret deleted, %s remains.\n", tc.expectedDeletedSecret)
				}
			}
		})
	}
}

func TestSetFromEnvInt(t *testing.T) {
	testCases := []struct {
		envVar       string
		defaultValue int
		expected     int
	}{
		{"", 5, 5},         // No Env variable set.
		{"6", 5, 6},        // Env variable set.
		{"DEADBEEF", 5, 5}, // Non integer env variable set.
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			envVarName := "TEST"
			os.Setenv(envVarName, tc.envVar)
			ret := setFromEnvInt(envVarName, tc.defaultValue)
			if ret != tc.expected {
				t.Errorf("Incorrect return value %d, expected %d.\n", ret, tc.expected)
			}
		})
	}
}

func TestPorter2k8sConfigMap(t *testing.T) {
	testCases := []struct {
		name     string
		original map[string]string
		data     map[string]interface{}
		expected map[string]string
	}{
		{
			"porter2k8s",
			map[string]string{},
			map[string]interface{}{"Test": "TEST"},
			map[string]string{"Test": "TEST"},
		},
		{
			"porter2k8s",
			map[string]string{"Test": "other"},
			map[string]interface{}{"Test": "TEST"},
			map[string]string{"Test": "other"},
		},
		{
			"porter2k8s",
			map[string]string{"Test": "other"},
			map[string]interface{}{"Test": "TEST"},
			map[string]string{"Test": "other"},
		},
		{
			"notPorter2k8s",
			map[string]string{},
			map[string]interface{}{"Test": "TEST"},
			map[string]string{},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			porter2k8sCM := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      tc.name,
						"namespace": "default",
					},
					"data": tc.data,
				},
			}
			gvrToListKind := map[schema.GroupVersionResource]string{
				{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
			}
			dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				gvrToListKind, &porter2k8sCM)
			mapper := restmapper.NewDiscoveryRESTMapper(resources)
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					Namespace: "default",
				},
				ClusterSettings: tc.original,
				DynamicClient:   dynamicClient,
				Logger:          logger,
				Mapper:          mapper,
			}
			regionEnv.porter2k8sConfigMap()
			data := regionEnv.ClusterSettings
			if !reflect.DeepEqual(data, tc.expected) {
				t.Errorf("Incorrect return value %+v, expected %+v.\n", data, tc.expected)
			}
		})
	}
}

func TestUpdateGateway(t *testing.T) {
	testCases := []struct {
		originalGateways    []string
		replacementGateways string
		expected            []string
	}{
		{[]string{"original"}, "replacement", []string{"replacement"}},
		{[]string{"original"}, "", []string{"original"}},
		{[]string{"original"}, "replacement1 replacement2", []string{"replacement1", "replacement2"}},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Setup.
			var regionEnv RegionEnv
			regionEnv.Region = "theBestRegion"
			regionEnv.Logger = logger
			testVirtualService := virtualService.DeepCopy()
			unstructured.SetNestedStringSlice(testVirtualService.Object, tc.originalGateways, "spec", "gateways")
			regionEnv.Unstructured = []*unstructured.Unstructured{testVirtualService}
			if tc.replacementGateways != "" {
				regionEnv.ClusterSettings = map[string]string{"GATEWAYS": tc.replacementGateways}
			}

			// Run updateRegistry.
			regionEnv.updateGateway()
			resultVS := regionEnv.findUnstructured("networking.istio.io/v1beta1", "VirtualService")
			resultGateways, found, _ := unstructured.NestedStringSlice(resultVS[0].Object, "spec", "gateways")
			if !found {
				t.Errorf("No gateways found in virtual service %+v", resultGateways)
			}
			for i, result := range resultGateways {
				if result != tc.expected[i] {
					t.Errorf("Incorrect return value %+v, expected %+v.\n", result, tc.expected)
				}
			}
		})
	}
}

func TestUpdateRegistry(t *testing.T) {
	testCases := []struct {
		name     string
		image    string
		registry string
		expected string
	}{
		{
			"test-app",
			"983073263818.dkr.ecr.us-west-2.amazonaws.com/test_org/test-app",
			"983073263818.dkr.ecr.ap-northeast-1.amazonaws.com",
			"983073263818.dkr.ecr.ap-northeast-1.amazonaws.com/test_org/test-app",
		},
		{
			"test-app",
			"983073263818.dkr.ecr.us-west-2.amazonaws.com/test_org/test-app",
			"mycontainerregistry082.azurecr.io",
			"mycontainerregistry082.azurecr.io/test_org/test-app",
		},
		{
			"test-app",
			"983073263818.dkr.ecr.us-west-2.amazonaws.com/test_org/test-app",
			"",
			"983073263818.dkr.ecr.us-west-2.amazonaws.com/test_org/test-app",
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Setup.
			var regionEnv RegionEnv
			regionEnv.Region = "theBestRegion"
			regionEnv.Logger = logger

			// Package up test deployment
			testDeployment := deployment.DeepCopy()
			testDeployment.SetName(tc.name)
			containers, _, _ := unstructured.NestedSlice(testDeployment.Object, "spec", "template", "spec", "containers")
			container := containers[0].(map[string]interface{})
			container["image"] = tc.image
			container["name"] = tc.name
			containers[0] = container
			unstructured.SetNestedSlice(testDeployment.Object, containers, "spec", "template", "spec", "containers")
			regionEnv.PodObject = testDeployment

			if tc.registry != "" {
				regionEnv.ClusterSettings = map[string]string{"REGISTRY": tc.registry}
			}

			// Run updateRegistry.
			regionEnv.updateRegistry()
			containers, _, _ = unstructured.NestedSlice(testDeployment.Object, "spec", "template", "spec", "containers")
			container = containers[0].(map[string]interface{})
			result := container["image"]
			if result != tc.expected {
				t.Errorf("Incorrect return value %+v, expected %+v.\n", result, tc.expected)
			}
		})
	}
}

func TestUpdateDomainName(t *testing.T) {
	testCases := []struct {
		newDomain string
		expected  []string
	}{
		{".us-west-3.micro.echosignstage.com", []string{".us-west-3.micro.echosignstage.com"}},
		{"", []string{".micro.test.com"}},
		{"-dev-va7.micro.echosign.com", []string{"-dev-va7.micro.echosign.com", "-dev-va7.int.micro.echosign.com"}},
		{
			".ap-northwest-1.micro.echosignstage.com,-an7.adobe.com",
			[]string{".ap-northwest-1.micro.echosignstage.com", "-an7.adobe.com"},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Setup.
			var regionEnv RegionEnv
			testGateway := gateway.DeepCopy()
			testVirtualService := virtualService.DeepCopy()
			regionEnv.Logger = logger
			regionEnv.Region = "theBestRegion"
			regionEnv.Unstructured = []*unstructured.Unstructured{
				ingressRouteInternal.DeepCopy(),
				ingressRouteExternal.DeepCopy(),
				testGateway.DeepCopy(),
				testVirtualService.DeepCopy(),
			}

			if tc.newDomain != "" {
				regionEnv.ClusterSettings = map[string]string{"DOMAIN": tc.newDomain}
			}

			// Run updateDomainName.
			regionEnv.updateDomainName()
			// Check Gateway.
			resultGateway := regionEnv.findUnstructured("networking.istio.io/v1beta1", "Gateway")[0]
			servers, _, _ := unstructured.NestedSlice(resultGateway.Object, "spec", "servers")
			for _, serverInterface := range servers {
				server, _ := serverInterface.(map[string]interface{})
				hosts, _, _ := unstructured.NestedStringSlice(server, "hosts")
				for _, host := range hosts {
					if !stringInExpected(host, tc.expected) {
						t.Errorf("Gateway incorrect host name %s, expected domain(s) %s.\n", host, tc.expected)
					}
				}
			}
			// Check Virtual Service.
			resultVS := regionEnv.findUnstructured("networking.istio.io/v1beta1", "VirtualService")[0]
			hosts, _, _ := unstructured.NestedStringSlice(resultVS.Object, "spec", "hosts")
			for _, host := range hosts {
				if !stringInExpected(host, tc.expected) {
					t.Errorf("Virtual Service incorrect host name %s, expected domain(s) %s.\n", host, tc.expected)
				}
			}

			// Only 1 domain name is allowed for ingress route
			if strings.Contains(tc.newDomain, ",") {
				return
			}
			// Check Virtual Host in IngressRoute(s)
			resultIngressRoutes := regionEnv.findUnstructured("contour.heptio.com/v1beta1", "IngressRoute")
			for _, ingressroute := range resultIngressRoutes {
				fqdn, _, _ := unstructured.NestedString(ingressroute.Object, "spec", "virtualhost", "fqdn")
				if !stringInExpected(fqdn, tc.expected) {
					t.Errorf(
						"IngressRoute incorrect host name %s, expected domain(s) %s.\n",
						fqdn,
						tc.expected,
					)
				}
			}
		})
	}
}

func TestHPAutoscalerDeepCopy(t *testing.T) {
	testCases := []struct {
		originalHPAVersion string
		copyHPAVersion     string
		expected           string
	}{
		{"original", "replacement", "original"},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Setup.
			original := horizontalPodAutoscaler
			original.SetResourceVersion(tc.originalHPAVersion)
			replica := original.DeepCopy()
			replica.SetResourceVersion(tc.copyHPAVersion)
			if original.GetResourceVersion() != tc.expected {
				t.Errorf("HPA DeepCopy failed. Expected %s, but received %s\n",
					tc.expected,
					original.GetResourceVersion(),
				)
			}
		})
	}
}

func TestUpdateHPAMinimum(t *testing.T) {
	testCases := []struct {
		clustermin    string
		clusterminmax string
		hPAMin        int64
		hPAMax        int64
		expectedMin   int64
		expectedMax   int64
	}{
		{"1", "", int64(1), int64(10), int64(1), int64(10)},
		{"2", "5", int64(1), int64(10), int64(2), int64(10)},
		{"3", "", int64(1), int64(2), int64(3), int64(3)},
		{"3", "5", int64(1), int64(2), int64(3), int64(3)},
		{"2", "2", int64(4), int64(10), int64(2), int64(10)},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Setup.
			var regionEnv RegionEnv
			if tc.clusterminmax != "" {
				regionEnv.ClusterSettings = map[string]string{"HPAMIN": tc.clustermin, "HPAMINMAX": tc.clusterminmax}
			} else {
				regionEnv.ClusterSettings = map[string]string{"HPAMIN": tc.clustermin}
			}
			regionEnv.Logger = logger
			unstructured.SetNestedField(horizontalPodAutoscaler.Object, tc.hPAMin, "spec", "minReplicas")
			unstructured.SetNestedField(horizontalPodAutoscaler.Object, tc.hPAMax, "spec", "maxReplicas")
			regionEnv.Unstructured = []*unstructured.Unstructured{&horizontalPodAutoscaler}
			regionEnv.updateHPAMinimum()
			minResult, _, _ := unstructured.NestedInt64(horizontalPodAutoscaler.Object, "spec", "minReplicas")
			maxResult, _, _ := unstructured.NestedInt64(horizontalPodAutoscaler.Object, "spec", "maxReplicas")
			if minResult != tc.expectedMin {
				t.Errorf("Incorrect minimum replica number %d, expected %d.\n", minResult, tc.expectedMin)
			}
			if maxResult != tc.expectedMax {
				t.Errorf("Incorrect maximum replica number %d, expected %d.\n", maxResult, tc.expectedMax)
			}
		})
	}
}

func TestReplaceHPAutoscalerTokens(t *testing.T) {
	testCases := []struct {
		metricKey   string
		metricValue string
		expected    string
	}{
		{"queue", "my-queue-name", "my-queue-name"},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			var regionEnv RegionEnv
			regionEnv.ClusterSettings = map[string]string{"QUEUE_NAME": tc.metricValue}
			regionEnv.Logger = logger
			regionEnv.replaceDynamicParameters(&horizontalPodAutoscalerTokens)
			metrics, _, _ := unstructured.NestedSlice(horizontalPodAutoscalerTokens.Object, "spec", "metrics")
			metric := metrics[0].(map[string]interface{})
			matchLabels, _, _ := unstructured.NestedStringMap(metric, "external", "metric", "selector", "matchLabels")
			t.Logf("Key %s", matchLabels[tc.metricKey])
			if matchLabels[tc.metricKey] != tc.expected {
				t.Errorf("Incorrect token replacement %s for key %s in HP Autoscaler",
					matchLabels[tc.metricKey],
					tc.metricKey,
				)
			}
			t.Logf("HPA with no external metrics configured to test for nil problems")
			regionEnv.replaceDynamicParameters(&horizontalPodAutoscaler)
		})
	}
}

func TestCalculateWatchTimeout(t *testing.T) {
	testCases := []struct {
		setProbe         bool
		buffer           int32
		initialDelay     int32
		failureThreshold int32
		timeout          int32
		period           int32
		expected         int64
	}{
		// No default values. buffer + initialDelay + failureThreshold*(period+timeout)
		{true, 180, 10, 4, 5, 15, 270},
		// All default values 0 + 3 *(1+10)
		{true, 0, 0, 0, 0, 0, 33},
		// Should return all default values, even though no liveness probe exists 0 + 3 *(1+10)
		{false, 0, 0, 0, 0, 0, 33},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			testDeployment := deployment.DeepCopy()
			// Setup.
			if tc.setProbe {
				livenessProbe := v1.Probe{
					InitialDelaySeconds: tc.initialDelay,
					TimeoutSeconds:      tc.timeout,
					PeriodSeconds:       tc.period,
					FailureThreshold:    tc.failureThreshold,
				}
				typedDeployment := appsv1.Deployment{}
				runtime.DefaultUnstructuredConverter.FromUnstructured(testDeployment.Object, &typedDeployment)
				typedDeployment.Spec.Template.Spec.Containers[0].LivenessProbe = &livenessProbe
				testDeployment.Object, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(&typedDeployment)
			}
			result := calculateWatchTimeout(testDeployment, tc.buffer)
			if *result != tc.expected {
				t.Errorf("Incorrect watch timeout %d, expected %d.\n", *result, tc.expected)
			}
		})
	}
}

func TestPrepareDeployment(t *testing.T) {
	testCases := []struct {
		image    string // image in deployment.yaml
		sha      string // sha passed as arg
		expected string
	}{
		{
			"test.acr-test.ecr56.edu/porter2k8s/org/testapp",
			"f69a8e3",
			"test.acr-test.ecr56.edu/porter2k8s/org/testapp:f69a8e3",
		}, // No image specified
		{
			"test.acr-test.ecr56.edu/porter2k8s/org/testapp:",
			"f69a8e3",
			"test.acr-test.ecr56.edu/porter2k8s/org/testapp:f69a8e3",
		}, // Trailing colon, no image specified
		{
			"test.acr-test.ecr56.edu/porter2k8s/org/testapp:6b87561",
			"f69a8e3",
			"test.acr-test.ecr56.edu/porter2k8s/org/testapp:f69a8e3",
		}, // Image specified
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Setup.
			var kubeObjects KubeObjects
			testDeployment := deployment.DeepCopy()
			typedDeployment := appsv1.Deployment{}
			runtime.DefaultUnstructuredConverter.FromUnstructured(testDeployment.Object, &typedDeployment)
			typedDeployment.Spec.Template.Spec.Containers[0].Image = tc.image
			testDeployment.Object, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(&typedDeployment)
			kubeObjects.PodObject = testDeployment
			err := kubeObjects.prepareDeployment(tc.sha)
			if err != nil {
				t.Errorf("Unexpected error: %s\n", err)
			}
			runtime.DefaultUnstructuredConverter.FromUnstructured(testDeployment.Object, &typedDeployment)
			imageName := typedDeployment.Spec.Template.Spec.Containers[0].Image
			if imageName != tc.expected {
				t.Errorf("Incorrect image name %s, expected %s.\n", imageName, tc.expected)
			}
			// Service Side Apply Bug fixed in 1.20. Need to add protocol to ports in the meantime.
			// https://github.com/kubernetes-sigs/structured-merge-diff/issues/130
			if typedDeployment.Spec.Template.Spec.Containers[0].Ports[0].Protocol == "" {
				t.Error("Port protocol not added. Remove this test when all servers are >= 1.20")
			}
		})
	}
}

func TestFindContainer(t *testing.T) {
	testDeployment := deployment.DeepCopy()
	typedDeployment := appsv1.Deployment{}
	runtime.DefaultUnstructuredConverter.FromUnstructured(testDeployment.Object, &typedDeployment)
	typedDeployment.Spec.Template.Spec.Containers[0].Name = "deploy"
	typedDeployment.ObjectMeta.Name = "deploy"
	testDeployment.Object, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(&typedDeployment)

	testJob := job.DeepCopy()
	typedJob := batchv1.Job{}
	runtime.DefaultUnstructuredConverter.FromUnstructured(testJob.Object, &typedJob)
	typedJob.Spec.Template.Spec.Containers[0].Name = "job"
	typedJob.ObjectMeta.Name = "job"
	testJob.Object, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(&typedJob)

	testStatefulSet := statefulSet.DeepCopy()
	typedStatefulSet := appsv1.StatefulSet{}
	runtime.DefaultUnstructuredConverter.FromUnstructured(testStatefulSet.Object, &typedStatefulSet)
	typedStatefulSet.Spec.Template.Spec.Containers[0].Name = "statefulSet"
	typedStatefulSet.ObjectMeta.Name = "statefulSet"
	testStatefulSet.Object, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(&typedStatefulSet)

	testCases := []struct {
		object   *unstructured.Unstructured
		expected string
	}{
		{testDeployment, "deploy"},
		{testJob, "job"},
		{testStatefulSet, "statefulSet"},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			container, _ := findContainer(tc.object)
			if container == nil || container.Name != tc.expected {
				t.Errorf("Unexpected value %v, expected %s", container, tc.expected)
			}
		})
	}
}

func TestInjectSecretKeyRefs(t *testing.T) {
	testCases := []struct {
		secretKeyRef SecretKeyRef
	}{
		{
			SecretKeyRef{
				Key:    "secret-key",
				Name:   "DB_SECRET",
				Secret: "super-secret",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Setup.
			testDeployment := deployment.DeepCopy()
			regionEnv := RegionEnv{
				PodObject:     testDeployment,
				SecretKeyRefs: []SecretKeyRef{tc.secretKeyRef},
				Logger:        logger,
			}
			regionEnv.injectSecretKeyRefs()
			if len(regionEnv.Errors) > 0 {
				t.Errorf("Unexpected error: %s\n", regionEnv.Errors[0])
			}
			typedDeployment := appsv1.Deployment{}
			runtime.DefaultUnstructuredConverter.FromUnstructured(testDeployment.Object, &typedDeployment)
			// Only 1 container and 1 environment variable.
			envVar := typedDeployment.Spec.Template.Spec.Containers[0].Env[0]
			if envVar.Name != tc.secretKeyRef.Name ||
				envVar.ValueFrom.SecretKeyRef.Name != tc.secretKeyRef.Secret ||
				envVar.ValueFrom.SecretKeyRef.Key != tc.secretKeyRef.Key {

				t.Errorf("Incorrect secretname injected into deployment %+v", envVar)
			}
		})
	}
}

func TestReplaceServiceInstanceParameters(t *testing.T) {
	testCases := []struct {
		parameters      string
		clusterSettings map[string]string
		expected        string
	}{
		{
			`/subs/{{.ACCOUNT}}/rg/{{.BASERESOURCEGROUP}}/{{.VNET}}/{{.VNET}}-Database-subnet`,
			map[string]string{"ACCOUNT": "abcdefg", "BASERESOURCEGROUP": "base-rg", "VNET": "test-vnet"},
			`/subs/abcdefg/rg/base-rg/test-vnet/test-vnet-Database-subnet`,
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			testServiceInstance := serviceInstance1.DeepCopy()
			unstructured.SetNestedField(testServiceInstance.Object, tc.parameters, "spec", "parameters", "subnetID")
			regionEnv := &RegionEnv{
				ClusterSettings: tc.clusterSettings,
				Logger:          logger,
			}
			regionEnv.replaceDynamicParameters(testServiceInstance)
			result, _, _ := unstructured.NestedString(testServiceInstance.Object, "spec", "parameters", "subnetID")
			if result != tc.expected {
				t.Errorf("Incorrect parameter replacement %s, expected %s.\n", result, tc.expected)
				t.Errorf("Error: %s", regionEnv.Errors)
			}
		})
	}
}

func TestGetConfig(t *testing.T) {
	// Ensure env variables from one region do not bleed into another.
	testCases := []struct {
		regions  string
		expected []string
	}{
		{
			"A B",
			[]string{"A", "B"},
		},
	}
	// Setup
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			cfg := &CmdConfig{
				ConfigType: "test",
				Regions:    tc.regions,
			}
			i := 0
			// TestEnv adds a single env variable for each region. Therefore there should only be one.
			for result := range getConfig(context.TODO(), cfg) {
				// Remove "ENV_VARS_TO_REDACT"
				for i, envVar := range result.Vars {
					if envVar.Name == "ENV_VARS_TO_REDACT" {
						result.Vars = append(result.Vars[:i], result.Vars[i+1:]...)
					}
				}
				if len(result.Vars) != 1 {
					t.Errorf("Unexpected number of Environment variables: %d, expected 1.\n", len(result.Vars))
				}
				if result.Vars[0].Value != tc.expected[i] {
					t.Errorf("Incorrect env var value %s, expected %s.\n", result.Vars[0].Value, tc.expected[i])
				}
				i++
			}
		})
	}
}

func TestIdentifySecrets(t *testing.T) {
	testCases := []struct {
		secrets  []SecretRef
		expected string
	}{
		{
			[]SecretRef{
				SecretRef{Name: "A_Secret", Path: "/", Value: ""},
			},
			"A_Secret",
		},
		{
			[]SecretRef{
				SecretRef{Name: "A_Secret", Path: "/", Value: ""},
				SecretRef{Name: "Another_-_Secret", Path: "/", Value: ""},
			},
			"A_Secret,Another_-_Secret",
		},
		{
			[]SecretRef{
				SecretRef{Name: "A_Secret", Path: "/", Value: ""},
				SecretRef{Name: "Another_-_Secret", Path: "/", Value: ""},
				SecretRef{Name: "Third+Secret", Path: "/", Value: ""},
			},
			"A_Secret,Another_-_Secret,Third+Secret",
		},
	}
	// Setup
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			regionEnv := &RegionEnv{
				Secrets: tc.secrets,
				Vars:    []EnvVar{},
				Logger:  logger,
			}
			regionEnv.identifySecrets()
			if regionEnv.Vars[0].Value != tc.expected {
				t.Errorf(
					"Unexpected value of ENV_VARS_TO_REDACT: %s, expected %s\n",
					regionEnv.Vars[0].Value,
					tc.expected,
				)
			}
		})
	}
}

func TestValidateAndTag(t *testing.T) {
	testCases := []struct {
		testName                  string
		deployName                string
		hPAName                   string
		hPATarget                 string
		serviceInstanceName1      string
		serviceInstanceName2      string
		serviceName               string
		serviceOverrideAnnotation string
		errorMessage              string
		expected                  string
	}{
		{
			"Valid",
			"testservice",
			"testserviceHPA",
			"testservice",
			"testserviceRDS",
			"testserviceSQL",
			"testservice",
			"",
			"",
			"testservice",
		},
		{
			"Valid because of annotation",
			"testdbschema",
			"testserviceHPA",
			"testdbschema",
			"testserviceRDS",
			"testserviceSQL",
			"",
			"test",
			"",
			"test",
		},
		{
			"Wrong HPA deployment reference",
			"testservice",
			"testserviceHPA",
			"otherservice",
			"testserviceRDS",
			"testserviceSQL",
			"testservice",
			"",
			"HPAutoscaler references",
			"testservice",
		},
		{
			"Wrong HPA name",
			"testservice",
			"otherserviceHPA",
			"testservice",
			"testserviceRDS",
			"testserviceSQL",
			"testservice",
			"",
			"does not contain service name",
			"testservice",
		},
		{
			"Wrong Service Instance name",
			"testservice",
			"testserviceHPA",
			"testservice",
			"testserviceRDS",
			"otherserviceSQL",
			"testservice",
			"",
			"does not contain service name",
			"testservice",
		},
		{
			"Worker without Service Object and wrong HPA name",
			"testworker",
			"testserviceHPA",
			"testworker",
			"",
			"",
			"",
			"",
			"does not contain service name",
			"testworker",
		},
		{
			"Worker without Service Object",
			"testservice",
			"testserviceHPA",
			"testservice",
			"testserviceRDS",
			"testserviceSQL",
			"",
			"",
			"",
			"testservice",
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s", tc.testName), func(t *testing.T) {
			test1 := serviceInstance1.DeepCopy()
			test2 := serviceInstance2.DeepCopy()
			test1.SetName(tc.serviceInstanceName1)
			test2.SetName(tc.serviceInstanceName2)
			hpaCopy := horizontalPodAutoscaler.DeepCopy()
			hpaCopy.SetName(tc.hPAName)
			unstructured.SetNestedField(hpaCopy.Object, tc.hPATarget, "spec", "scaleTargetRef", "name")

			nonPodObjects := []*unstructured.Unstructured{
				ingressRouteInternal.DeepCopy(),
				ingressRouteExternal.DeepCopy(),
				hpaCopy,
			}

			if tc.serviceName != "" {
				service := serviceRunning.DeepCopy()
				service.SetName(tc.serviceName)
				nonPodObjects = append(nonPodObjects, service)
			}

			testDeployment := deployment.DeepCopy()
			testDeployment.SetName(tc.deployName)
			if tc.serviceOverrideAnnotation != "" {
				t.Log("setting annotation")
				annotations := map[string]string{"porter2k8s/service-name": tc.serviceOverrideAnnotation}
				testDeployment.SetAnnotations(annotations)
			}
			kubeObjects := KubeObjects{
				PodObject: testDeployment,
				Unstructured: map[string][]*unstructured.Unstructured{
					"all": nonPodObjects,
					"aws": []*unstructured.Unstructured{
						test1,
						test2,
					},
				},
			}
			err := kubeObjects.validateAndTag()
			t.Logf("Error %s", err)

			if err != nil {
				if tc.errorMessage == "" {
					t.Errorf("Unexpected error message: %s, none expected.", err)
				} else if !strings.Contains(err.Error(), tc.errorMessage) {
					t.Errorf("Unexpected error message: %s, expected %s", err, tc.errorMessage)
				}
			} else {
				// Verify all objects have been tagged.
				serviceName1 := test1.GetLabels()["app"]
				serviceName2 := test2.GetLabels()["app"]
				hpaName := hpaCopy.GetLabels()["app"]
				if hpaName != tc.expected {
					t.Errorf("HP AutoScaler not tagged with service name, got %s, expected %s", hpaName, tc.expected)
				} else if serviceName1 != tc.expected {
					t.Errorf("Service Instance not tagged with service name, got %s, expected %s", serviceName1,
						tc.expected)
				} else if serviceName2 != tc.expected {
					t.Errorf("Service Instance not tagged with service name, got %s, expected %s", serviceName2,
						tc.expected)
				}
			}
		})
	}
}

func TestValidateSecretPath(t *testing.T) {
	testCases := []struct {
		secrets             SecretRef
		serviceName         string
		secretPathWhiteList string
		errorExpected       bool
	}{
		{

			SecretRef{
				Name:  "A_Secret",
				Path:  "Ethos/tenants/SignMicroservices/common/preview",
				Value: "pass",
			},
			"msc",
			"COMMON 3rdParty",
			false,
		},
		{

			SecretRef{
				Name:  "B_Secret",
				Path:  "Ethos/tenants/common123/preview",
				Value: "pass",
			},
			"common-123", // Dashes are ignored
			"3rdParty",
			false,
		},
		{

			SecretRef{
				Name:  "C_Secret",
				Path:  "Ethos/tenants/azure/preview/servicetest",
				Value: "pass",
			},
			"servicetesting",
			"SignMicrosevices common",
			true,
		},
		{

			SecretRef{
				Name:  "D_Secret",
				Path:  "Ethos/tenants/azure/preview/servicetest",
				Value: "pass",
			},
			"msc",
			"", // No check if whitelist not provided
			false,
		},
		{

			SecretRef{
				Name:  "E_Secret",
				Path:  "Ethos/tenants/azure/preview/servicetest",
				Value: "pass",
			},
			"servicetesting",
			"SignMicrosevices",
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			regionEnv := RegionEnv{
				Secrets: []SecretRef{tc.secrets},
				Cfg: &CmdConfig{
					SecretPathWhiteList: tc.secretPathWhiteList,
				},
				Logger: logger,
			}
			output := regionEnv.validateSecretPath(tc.serviceName)
			result := output != nil
			if result != tc.errorExpected {
				t.Errorf("Error expected: %t Returned error %s", tc.errorExpected, output)
			}
		})
	}
}

func stringInExpected(result string, expectedList []string) bool {
	found := false
	for _, expected := range expectedList {
		if strings.Contains(result, expected) {
			found = true
		}
	}
	return found
}

func TestReplaceNamedSecretData(t *testing.T) {
	testCases := []struct {
		secret          *unstructured.Unstructured
		clusterSettings map[string]string
		expected        string
		errorExpected   bool
	}{
		{
			namedSecrets.DeepCopy(),
			map[string]string{"SECRET_VALUE": "werewolvesnotswearwolves"},
			"werewolvesnotswearwolves",
			false,
		},
		{
			namedSecretsError.DeepCopy(),
			map[string]string{"SECRET_VALUE": "thisvaluedoesnotmatter"},
			"U3RhdGljU2VjcmV0Rm9yYmlkZGVuCg==",
			true,
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			regionEnv := &RegionEnv{
				ClusterSettings: tc.clusterSettings,
				Unstructured:    []*unstructured.Unstructured{tc.secret},
				Logger:          logger,
			}
			secretEntry := "mysecret"
			regionEnv.replaceDynamicParameters(tc.secret)
			data, _, _ := unstructured.NestedStringMap(regionEnv.Unstructured[0].Object, "data")
			if data[secretEntry] != tc.expected {
				t.Errorf("Incorrect value replacement %s, expected %s.", data[secretEntry], tc.expected)
			} else if len(regionEnv.Errors) > 0 && tc.errorExpected != true {
				t.Errorf("Unexpected error encountered with key: %s, value: %s", secretEntry, data[secretEntry])
			}
		})
	}
}
