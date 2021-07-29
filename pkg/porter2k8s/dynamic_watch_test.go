/*
Copyright 2021 Adobe. All rights reserved.
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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var redisWatch = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "azure.microsoft.com/v1alpha1",
		"kind":       "RedisCache",
		"metadata": map[string]interface{}{
			"name":      "myRedis",
			"namespace": "test",
		},
		"spec": map[string]interface{}{
			"location": "westeurope",
		},
		"status": map[string]interface{}{
			"message":     "successfully provisioned",
			"output":      "10.20.44.132",
			"provisioned": true,
			"requested":   "2021-01-28T19:40:39Z",
			"resourceId":  "/subscriptions/123/resourceGroups/rg/providers/Microsoft.Cache/Redis/myRedis",
			"state":       "Succeeded",
		},
	},
}

var dynamoWatch = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "dynamodb.services.k8s.aws/v1alpha1",
		"kind":       "Table",
		"metadata": map[string]interface{}{
			"name":      "myDynamo",
			"namespace": "test",
		},
		"spec": map[string]interface{}{
			"attributeDefinitions": []map[string]interface{}{
				{
					"attributeName": "agreement_id",
					"attributeType": "S",
				},
			},
			"keySchema": []map[string]interface{}{
				{
					"attributeName": "agreement_id",
					"keyType":       "HASH",
				},
			},
			"provisionedThroughput": map[string]interface{}{
				"readCapacityUnits":  30,
				"writeCapacityUnits": 30,
			},
			"tableName": "agreementmetadata",
		},
		"status": map[string]interface{}{
			"ackResourceMetadata": map[string]interface{}{
				"arn":            "arn:aws:dynamodb:us-east-1:12345:table/myDynamo",
				"ownerAccountID": "$(AWS_ACCOUNT_ID)",
			},
			"conditions":       []string{},
			"creationDateTime": "2021-04-01T22:22:02Z",
			"itemCount":        0,
			"tableID":          "0123456789",
			"tableSizeBytes":   0,
			"tableStatus":      "ACTIVE",
		},
	},
}

func TestNewWatchConfigStatefulSet(t *testing.T) {
	testCases := []struct {
		previous         bool
		expectedReplicas int64
	}{
		{true, 4},
		{false, 1},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			service := statefulSet.DeepCopy()
			previous := &unstructured.Unstructured{}
			if tc.previous {
				previous = statefulSet.DeepCopy()
				unstructured.SetNestedField(previous.Object, tc.expectedReplicas, "spec", "replicas")
			}
			conf := NewWatchConfig(service, previous, 30)
			readyReplicas := conf.Success.Conditions[0].StateInt64
			if readyReplicas != tc.expectedReplicas {
				t.Errorf("Unexpected readyReplica count %d. Expected %d", readyReplicas, tc.expectedReplicas)
			}
			t.Logf("WatchConfig %+v", conf)
		})
	}
}

func TestNewWatchConfig(t *testing.T) {
	testCases := []struct {
		version        string
		kind           string
		buffer         int32
		expTimeout     int64
		expSuccessCond int
		expFailureCond int
		expLogic       ConditionLogic
	}{
		// Deployment and StatefulSet timeouts are more complicated and tested as part of TestCalculateWatchTimeout
		{"dynamodb.services.k8s.aws/v1alpha1", "Table", 0, 600, 1, 1, AndLogic},
		{"azure.microsoft.com/v1alpha1", "RedisCache", 0, 900, 1, 2, AndLogic},
		{"azure.microsoft.com/v1alpha1", "CosmosDB", 0, 900, 1, 2, AndLogic},
		{"azure.microsoft.com/v1alpha1", "ResourceGroup", 0, 120, 1, 2, AndLogic},
		{"azure.microsoft.com/v1alpha2", "MySQLServer", 0, 1800, 1, 2, AndLogic},
		{"monitoring.coreos.com/v1", "ServiceMonitor", 0, 0, 0, 0, AndLogic},
		{"servicecatalog.k8s.io/v1beta1", "ServiceInstance", 0, 3000, 1, 2, AndLogic},
		{"batch/v1", "Job", 30, 30, 1, 0, AndLogic},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			service := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": tc.version,
					"kind":       tc.kind,
					"metadata": map[string]interface{}{
						"name": "testApp",
					},
				},
			}
			previous := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": tc.version,
					"kind":       tc.kind,
					"metadata": map[string]interface{}{
						"name": "testApp",
					},
					"spec": map[string]interface{}{
						"replicas": 5,
					},
				},
			}
			conf := NewWatchConfig(&service, &previous, tc.buffer)

			if *conf.TimeoutSeconds != tc.expTimeout {
				t.Errorf("Unexpected watch timeout %d, expected %d", *conf.TimeoutSeconds, tc.expTimeout)
			}
			if len(conf.Success.Conditions) != tc.expSuccessCond {
				t.Errorf("Unexpected number of success conditions %d, expected %d", len(conf.Success.Conditions),
					tc.expSuccessCond)
			}
			if len(conf.Failure.Conditions) != tc.expFailureCond {
				t.Errorf("Unexpected number of failure conditions %d, expected %d", len(conf.Failure.Conditions),
					tc.expFailureCond)
			}
			if conf.Success.Logic != tc.expLogic {
				t.Errorf("Unexpected logical operator %d, expected %d", conf.Success.Logic, tc.expLogic)
			}
		})
	}
}

func TestCompareStatusMaps(t *testing.T) {
	testCases := []struct {
		status       map[string]interface{}
		conditionMap map[string]interface{}
		expected     bool
	}{
		{
			map[string]interface{}{"message": "Irrelevant nonesense", "type": "Failed", "status": "True"},
			map[string]interface{}{"type": "Failed", "status": "True"},
			true,
		},
		{
			map[string]interface{}{"message": "Irrelevant nonesense", "type": "Failed", "status": "True"},
			map[string]interface{}{"type": "Ready", "status": "True"},
			false,
		},
		{
			map[string]interface{}{"message": "Irrelevant nonesense", "type": "Failed", "status": "True"},
			map[string]interface{}{"type": "Failed", "missingKey": "True"},
			false,
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			result := compareStatusMaps(tc.status, tc.conditionMap)
			if result != tc.expected {
				t.Errorf("Unexpected comparison result. Expected %t", tc.expected)
			}
		})
	}
}
