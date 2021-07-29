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
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/restmapper"
)

var resourceGroup = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "azure.microsoft.com/v1alpha1",
		"kind":       "ResourceGroup",
		"metadata": map[string]interface{}{
			"name":      "resource group",
			"namespace": "test",
		},
		"spec": map[string]interface{}{
			"location": "{{.region}}",
		},
	},
}

var resourceGroup2 = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "azure.microsoft.com/v1alpha1",
		"kind":       "ResourceGroup",
		"metadata": map[string]interface{}{
			"name":      "resource group",
			"namespace": "test",
		},
		"spec": map[string]interface{}{
			"parameters": map[string]interface{}{
				"location": "{{.region}}-catmanagement",
			},
			"otherstuff": map[string]interface{}{
				"cloud": "*-nimbus",
			},
		},
	},
}

var cacheSubnetGroup = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "dynamodb.services.k8s.aws/v1alpha1",
		"kind":       "CacheSubnetGroup",
		"metadata": map[string]interface{}{
			"name":      "cacheSubnetGroup",
			"namespace": "test",
		},
		"spec": map[string]interface{}{
			"cacheSubnetGroupDescription": "Test Subnet Group",
			"cacheSubnetGroupName":        "test",
			"subnetIDs":                   `{{ splitList "," .support_subnets | toJson }}`,
		},
	},
}

var redis = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "azure.microsoft.com/v1alpha1",
		"kind":       "RedisCache",
		"metadata": map[string]interface{}{
			"name":      "redis",
			"namespace": "test",
		},
		"spec": map[string]interface{}{
			"location":      "{{.region}}",
			"resourceGroup": "resource group",
			"properties": map[string]interface{}{
				"sku": map[string]interface{}{
					"name":     "Cloud",
					"family":   "F",
					"capacity": "300",
				},
				"enableNonSslPort": "false",
				"subnetID": "/subscriptions/{{.ACCOUNT}}/resourceGroups/{{.BASE_RESOURCE_GROUP}}/providers" +
					"/Microsoft.Network/virtualNetworks/{{.VNET}}/subnets/MicroSubnet",
			},
		},
	},
}

var cosmosdb = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "azure.microsoft.com/v1alpha1",
		"kind":       "CosmosDB",
		"metadata": map[string]interface{}{
			"name":      "cosmosdb",
			"namespace": "test",
		},
		"spec": map[string]interface{}{
			"kind":          "GlobalDocumentDB",
			"location":      "{{.region}}",
			"resourceGroup": "resource group",
			"ipRules": []interface{}{
				"104.42.195.92",
				"40.76.54.131",
				"52.176.6.30",
				"52.169.50.45",
				"52.187.184.26",
			},
			"properties": map[string]interface{}{
				"capabilities": []interface{}{
					map[string]interface{}{
						"name": "EnableTable",
					},
				},
				"databaseAccountOfferType":      "Standard",
				"isVirtualNetworkFilterEnabled": true,
			},
			"virtualNetworkRules": []interface{}{
				map[string]interface{}{
					"subnetID": "/subscriptions/{{.MS_ACCOUNT}}/resourceGroups/{{.MS_BASE_RESOURCE_GROUP}}/providers" +
						"/Microsoft.Network/virtualNetworks/{{.MS_VNET}}/subnets/cosmosdb",
				},
			},
		},
	},
}

var serviceAccount = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{"app": "A"},
			"name":   "A",
			"annotations": map[string]interface{}{
				"eks.amazonaws.com/role-arn": "arn:aws:iam::{{.account}}:role/test-{{.environment}}-{{.region}}",
			},
		},
		"secrets": []interface{}{"test-token-abdce"},
	},
}

func TestReplaceDynamicParameters(t *testing.T) {
	testCases := []struct {
		object   *unstructured.Unstructured
		tokens   map[string]string
		path     []string
		subpath  []string
		expected string
	}{
		{
			&resourceGroup,
			map[string]string{"region": "europe"},
			[]string{"spec", "location"},
			[]string{},
			"europe",
		},
		{
			resourceGroup2.DeepCopy(),
			map[string]string{"region": "europe"},
			[]string{"spec", "parameters", "location"},
			[]string{},
			"europe-catmanagement",
		},
		{
			resourceGroup2.DeepCopy(),
			map[string]string{"region": "europe"},
			[]string{"spec", "otherstuff", "cloud"},
			[]string{},
			"*-nimbus",
		},
		{
			&redis,
			map[string]string{"region": "asia", "ACCOUNT": "0123456789", "BASE_RESOURCE_GROUP": "base_rg",
				"VNET": "my_vnet"},
			[]string{"spec", "properties", "subnetID"},
			[]string{},
			"/subscriptions/0123456789/resourceGroups/base_rg/providers/Microsoft.Network/virtualNetworks/my_vnet/" +
				"subnets/MicroSubnet",
		},
		{
			&cosmosdb,
			map[string]string{"region": "asia", "MS_ACCOUNT": "0123456789", "MS_BASE_RESOURCE_GROUP": "base_rg",
				"MS_VNET": "my_vnet"},
			[]string{"spec", "virtualNetworkRules"},
			[]string{"subnetID"},
			"/subscriptions/0123456789/resourceGroups/base_rg/providers/Microsoft.Network/virtualNetworks/my_vnet/" +
				"subnets/cosmosdb",
		},
		{
			&cacheSubnetGroup,
			map[string]string{"support_subnets": "subnet-1234,subnet-24500"},
			[]string{"spec", "subnetIDs"},
			[]string{},
			"subnet-1234",
		},
		{
			&serviceAccount,
			map[string]string{"account": "0123456789", "environment": "dev", "region": "east"},
			[]string{"metadata", "annotations", "eks.amazonaws.com/role-arn"},
			[]string{},
			"arn:aws:iam::0123456789:role/test-dev-east",
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			regionEnv := RegionEnv{
				ClusterSettings: tc.tokens,
				Logger:          logger,
			}
			regionEnv.replaceDynamicParameters(tc.object)
			result := findUnstructuredString(tc.object.Object, tc.path, tc.subpath)
			if result != tc.expected {
				t.Errorf("Unexpected result %s, expected %s\n", result, tc.expected)
			}
		})
	}
}

func TestSortUnstructured(t *testing.T) {
	testCases := []struct {
		objs     []string
		istio    string
		expected []string
	}{
		{
			[]string{"v1/Service", "networking.istio.io/v1beta1/VirtualService"},
			"true",
			[]string{"v1/Service", "networking.istio.io/v1beta1/VirtualService"},
		},
		{
			[]string{"v1/Service", "networking.istio.io/v1beta1/VirtualService"},
			"false",
			[]string{"v1/Service"},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			clusterSettings := map[string]string{"ISTIO": tc.istio}
			regionEnv := RegionEnv{
				ClusterSettings: clusterSettings,
				Unstructured:    stringToUnstructured(tc.objs),
			}
			regionEnv.sortUnstructured()
			result := unstructuredToString(regionEnv.Unstructured)
			if !cmp.Equal(result, tc.expected) {
				t.Errorf("Unexpected result %v expected %v\n", result, tc.expected)
			}
		})
	}
}

func TestAddSidecarContainer(t *testing.T) {
	testCases := []struct {
		clusterSettings map[string]string
		tailBufLimit    string
		sourcetype      string
	}{
		{
			map[string]string{},
			"145MB",
			"dc-k8s-asr",
		}, // No template values.
		{
			map[string]string{"LOGGING_TAIL_BUF_LIMIT": "100MB", "LOGGING_SOURCETYPE": "stage-source"},
			"100MB",
			"stage-source",
		}, // Template values to validate token replacement.
	}
	// Read "test/sidcar.yaml" into the "SIDECAR" key.
	decoded, _ := multiDecoder("../../test/sidecar.yaml")
	porter2k8sCM, _ := serviceFromObject(decoded[0].Object, nil)
	porter2k8sData, _, _ := unstructured.NestedStringMap(porter2k8sCM.Object, "data")
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			tc.clusterSettings["SIDECAR"] = porter2k8sData["SIDECAR"]
			regionEnv := RegionEnv{
				ClusterSettings: tc.clusterSettings,
				Logger:          logger,
				PodObject:       deployment.DeepCopy(),
			}
			regionEnv.addSidecarContainer()
			if len(regionEnv.Errors) > 0 {
				t.Error(regionEnv.Errors[0])
				return
			}
			regionEnv.replaceDynamicParameters(regionEnv.PodObject)
			if len(regionEnv.Errors) > 0 {
				t.Error(regionEnv.Errors[0])
				return
			}
			typedPodTemplate, _ := findPodTemplateSpecUnstructured(regionEnv.PodObject)
			// Ensure there are 2 containers and 3 volumes
			containers := typedPodTemplate.Spec.Containers
			if len(containers) != 2 {
				t.Errorf("Unexpected number of containers %d, expected 2", len(containers))
			}
			// Logging container is the 2nd one.
			for _, envVar := range containers[1].Env {
				if envVar.Name == "TAIL_BUF_LIMIT" && envVar.Value != tc.tailBufLimit {
					t.Errorf("Unexpected value of TAIL_BUF_LIMIT, result %s expected %s", envVar.Value, tc.tailBufLimit)
				} else if envVar.Name == "SPLUNK_SOURCETYPE" && envVar.Value != tc.sourcetype {
					t.Errorf("Unexpected value of SPLUNK_SOURCETYPE, result %s expected %s", envVar.Value, tc.sourcetype)
				}
			}
			volumes := typedPodTemplate.Spec.Volumes
			if len(volumes) != 3 {
				t.Errorf("Unexpected number of volumes %d, expected 3", len(containers))
			}
		})
	}
}

func TestCreateObjectSecrets(t *testing.T) {
	testCases := []struct {
		paths     []string
		keys      []string
		objectMap map[string]interface{}
		attempt   int
		expected  []string
		expectErr bool
	}{
		{ // Basic case.
			[]string{".status.url"},
			[]string{".status.url"},
			map[string]interface{}{
				"url": "www.example.com",
			},
			0,
			[]string{"www.example.com"},
			false,
		},
		{ // Multiple paths.
			[]string{".status.url", ".status.key"},
			[]string{".status.url", ".status.key"},
			map[string]interface{}{
				"url": "www.example.com",
				"key": "1234",
			},
			0,
			[]string{"www.example.com", "1234"},
			false,
		},
		{ // path with slices
			[]string{".status.nodeGroups[0].nodeGroupMembers[0].readEndpoint.address"},
			[]string{".status.nodeGroups0.nodeGroupMembers0.readEndpoint.address"},
			map[string]interface{}{
				"nodeGroups": []interface{}{
					map[string]interface{}{
						"nodeGroupID": "0001",
						"nodeGroupMembers": []interface{}{
							map[string]interface{}{
								"cacheClusterID": "test-001",
								"cacheNodeID":    "0001",
								"readEndpoint": map[string]interface{}{
									"address": "test.use1.cache.amazonaws.com",
									"port":    "6379",
								},
							},
						},
					},
				},
			},
			0,
			[]string{"test.use1.cache.amazonaws.com"},
			false,
		},
		{ // more complex jq query
			[]string{`.status.nodeGroups[] | select(.nodeGroupID == "0001") | .nodeGroupMembers[] | ` +
				`select(.cacheNodeID == "0001") | .readEndpoint.address`},
			[]string{`.status.nodeGroupsselect.nodeGroupID0001.nodeGroupMembersselect.cacheNodeID0001.readEndpoint.address`},
			map[string]interface{}{
				"nodeGroups": []interface{}{
					map[string]interface{}{
						"nodeGroupID": "0001",
						"nodeGroupMembers": []interface{}{
							map[string]interface{}{
								"cacheClusterID": "test-001",
								"cacheNodeID":    "0001",
								"readEndpoint": map[string]interface{}{
									"address": "test.use1.cache.amazonaws.com",
									"port":    "6379",
								},
							},
						},
					},
				},
			},
			0,
			[]string{"test.use1.cache.amazonaws.com"},
			false,
		},
		{ // missing status item
			[]string{`.status.nodeGroups[] | select(.nodeGroupID == "0001") | .nodeGroupMembers[] | ` +
				`select(.cacheNodeID == "0001") | .readEndpoint.address`},
			[]string{`.status.nodeGroupsselect.nodeGroupID0001.nodeGroupMembersselect.cacheNodeID0001.readEndpoint.address`},
			map[string]interface{}{
				"nodeGroups": []interface{}{
					map[string]interface{}{
						"nodeGroupID": "0001",
					},
				},
			},
			0, // Fail
			[]string{""},
			true,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "secrets"}: "SecretList",
	}
	defer cancel()
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			object := redis.DeepCopy()
			unstructured.SetNestedMap(object.Object, tc.objectMap, "status")
			refs := map[string][]string{"redis-rediscache": tc.paths}
			dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				gvrToListKind, &configMapList)
			mapper := restmapper.NewDiscoveryRESTMapper(resources)
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					Namespace: "",
				},
				Context:       ctx,
				DynamicClient: dynamicClient,
				Logger:        logger,
				Mapper:        mapper,
				ObjectRefs:    refs,
				PodObject:     deployment.DeepCopy(),
				Unstructured:  []*unstructured.Unstructured{object},
			}
			gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
			mapping, _ := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			regionEnv.createObjectSecrets(object, tc.attempt)

			if len(regionEnv.Errors) > 0 {
				if !tc.expectErr {
					t.Errorf("Unexpected Error %s", regionEnv.Errors[0])
				}
				return
			}

			dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace("")
			resultSecret, getErr := dynamicInterface.Get(ctx, "redis-rediscache", metav1.GetOptions{})

			if getErr != nil {
				t.Errorf("Secret missing %s", getErr.Error())
				return
			}
			secretData, _, _ := unstructured.NestedStringMap(resultSecret.Object, "data")
			for i, key := range tc.keys {
				secretValue, _ := base64.StdEncoding.DecodeString(secretData[key])
				if string(secretValue) != tc.expected[i] {
					t.Errorf("Unexpected result %v expected %v\n", string(secretValue), tc.expected[i])
				}
			}
		})
	}
}

func stringToUnstructured(objs []string) []*unstructured.Unstructured {
	var result []*unstructured.Unstructured
	for _, object := range objs {
		lastIndex := strings.LastIndex(object, "/")
		apiVersion := object[:lastIndex]
		kind := object[lastIndex+1:]
		unstruct := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": apiVersion,
				"kind":       kind,
			},
		}
		result = append(result, &unstruct)
	}
	return result
}

func unstructuredToString(objs []*unstructured.Unstructured) []string {
	var result []string
	for _, object := range objs {
		str := fmt.Sprintf("%s/%s", object.GetAPIVersion(), object.GetKind())
		result = append(result, str)
	}
	return result
}

func findUnstructuredString(obj map[string]interface{}, path, subpath []string) string {
	result, ok, _ := unstructured.NestedString(obj, path...)
	if ok {
		return result
	}
	resultStringSlice, ok, _ := unstructured.NestedStringSlice(obj, path...)
	if ok {
		return resultStringSlice[0]
	}
	resultSlice, ok, _ := unstructured.NestedSlice(obj, path...)
	if ok {
		subObject := resultSlice[0].(map[string]interface{})
		return findUnstructuredString(subObject, subpath, []string{})
	}
	return ""
}
