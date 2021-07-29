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
	"bytes"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var serviceRunning = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":              "running",
			"namespace":         "test",
			"creationTimestamp": "2004-01-01T00:00:00Z",
			"labels": map[string]interface{}{
				"app": "testapp",
			},
			"resourceVersion": "612488",
			"selfLink":        "/api/v1/namespaces/test/services/testapp",
			"uid":             "871f9449-4f4e-11e9-a995-d2a4ff4cc534",
		},
		"spec": map[string]interface{}{
			"ports": []interface{}{
				map[string]interface{}{
					"name":       "http",
					"protocol":   "TCP",
					"targetPort": "8080",
				},
			},
			"selector": map[string]interface{}{
				"app": "testapp",
			},
			"sessionAffinity": "None",
			"type":            "ClusterIP",
			"clusterIP":       "127.0.0.1",
		},
	},
}

var serviceUpdateNeededValue = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      "no update",
			"namespace": "test",
			"labels": map[string]interface{}{
				"app": "testapp",
			},
		},
		"spec": map[string]interface{}{
			"ports": []interface{}{
				map[string]interface{}{
					"name":       "http",
					"protocol":   "TCP",
					"targetPort": 8083,
				},
			},
			"selector": map[string]interface{}{
				"app": "testapp",
			},
			"sessionAffinity": "None",
			"type":            "ClusterIP",
		},
	},
}

var serviceUpdateNeededList = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      "needed list",
			"namespace": "test",
			"labels": map[string]interface{}{
				"app": "testapp",
			},
		},
		"spec": map[string]interface{}{
			"ports": []interface{}{
				map[string]interface{}{
					"name":       "http",
					"protocol":   "TCP",
					"targetPort": 8080,
				},
				map[string]interface{}{
					"name":       "https",
					"protocol":   "TCP",
					"targetPort": 8083,
				},
			},
			"selector": map[string]interface{}{
				"app": "testapp",
			},
			"sessionAffinity": "None",
			"type":            "ClusterIP",
		},
	},
}

var serviceUpdateNeededMap = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      "needed map",
			"namespace": "test",
			"labels": map[string]interface{}{
				"app": "testapp",
			},
		},
		"spec": map[string]interface{}{
			"ports": []interface{}{
				map[string]interface{}{
					"name":       "http",
					"protocol":   "TCP",
					"targetPort": 8080,
				},
			},
			"selector": map[string]interface{}{
				"app": "testapp",
				"new": "value",
			},
			"sessionAffinity": "None",
			"type":            "ClusterIP",
		},
	},
}

var serviceUpdateNotNeeded = unstructured.Unstructured{
	Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      "not needed",
			"namespace": "test",
			"labels": map[string]interface{}{
				"app": "testapp",
			},
		},
		"spec": map[string]interface{}{
			"ports": []interface{}{
				map[string]interface{}{
					"name":       "http",
					"protocol":   "TCP",
					"targetPort": 8080,
				},
			},
			"selector": map[string]interface{}{
				"app": "testapp",
				"new": "value",
			},
			"sessionAffinity": "None",
			"type":            "ClusterIP",
		},
	},
}

var objectReferenceA = v1.ObjectReference{
	Name: "snip",
}
var objectReferenceB = v1.ObjectReference{
	Name: "snap",
}
var objectReferenceC = v1.ObjectReference{
	Name: "snurr",
}

func TestMultiDecoder(t *testing.T) {
	testCases := []struct {
		filename     string
		objectNumber int
	}{
		{"../../test/istio.yaml", 2},
		{"../../test/secret.yaml", 1},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			pwd, _ := os.Getwd()
			filePath := fmt.Sprintf("%s/%s", pwd, tc.filename)
			objects, err := multiDecoder(filePath)
			t.Logf("objects %s\nerr %s\n", objects, err)
			numObjects := len(objects)
			if tc.objectNumber != numObjects {
				t.Errorf("Incorrect number of objects %d, expected %d\n", numObjects, tc.objectNumber)
			}
		})
	}
}

func TestInt64Ref(t *testing.T) {
	testCases := []struct {
		input    interface{}
		expected int64
	}{
		{"2", int64(2)},
		{2, int64(2)},
		{int32(2), int64(2)},
		{"a", int64(0)},
		{false, int64(0)},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			result := int64Ref(tc.input)
			if *result != tc.expected {
				t.Errorf("Unexpected result from input %d, expect %d \n", *result, tc.expected)
			}
		})
	}
}

func TestInt32Ref(t *testing.T) {
	testCases := []struct {
		input    interface{}
		expected int32
	}{
		{"2", int32(2)},
		{2, int32(2)},
		{int64(2), int32(2)},
		{"a", int32(0)},
		{false, int32(0)},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			result := int32Ref(tc.input)
			if *result != tc.expected {
				t.Errorf("Unexpected result from input %d, expect %d \n", *result, tc.expected)
			}
		})
	}
}

func TestAppendLabel(t *testing.T) {
	var obj v1.PodTemplateSpec
	appendLabel(&obj, "testKey", "testVal")

	expected := map[string]string{"testKey": "testVal"}
	if !reflect.DeepEqual(obj.ObjectMeta.Labels, expected) {
		t.Fatalf("Labels map not initialized %v, expect %v\n", obj.ObjectMeta.Labels, expected)
	}

	appendLabel(&obj, "secondKey", "secondVal")
	expected2 := map[string]string{"testKey": "testVal", "secondKey": "secondVal"}
	if !reflect.DeepEqual(obj.ObjectMeta.Labels, expected2) {
		t.Fatalf("Labels map not initialized %v, expect %v\n", obj.ObjectMeta.Labels, expected2)
	}
}

func TestTokenReplace(t *testing.T) {
	testCases := []struct {
		format   []byte // byte slice with tokens to be replaced.
		tokens   map[string]string
		expected []byte
	}{
		{
			[]byte("arn:aws:iam::{{.account}}:role/mobilesigcapture-{{.environment}}-{{.region}}"),
			map[string]string{"account": "983073263818", "environment": "preview", "region": "us-west-2"},
			[]byte("arn:aws:iam::983073263818:role/mobilesigcapture-preview-us-west-2"),
		}, // Standard use.
		{
			[]byte("{{.editor}} is better than {{.os}}."),
			map[string]string{"editor": "Vim", "os": "emacs"},
			[]byte("Vim is better than emacs."),
		}, // Random use.
		{
			[]byte("arn:aws:iam::{{.account}}:role/mobilesigcapture-{{.environment}}-{{.region}}"),
			map[string]string{"account": "983073263818", "region": "us-west-2"},
			[]byte("arn:aws:iam::983073263818:role/mobilesigcapture--us-west-2"),
		}, // Missing environment. No longer returns original.
		// Missing keys must be treated as zero values for default function to work.
		{
			[]byte("{{ .support_subnets | splitList \",\" }}"),
			map[string]string{"account": "983073263818", "support_subnets": "subnet-1932,subnet-9876"},
			[]byte("[subnet-1932 subnet-9876]"),
		}, // Sprig Function
		{
			[]byte("\"{{.RDS_REGION}}-catmanagement\""),
			map[string]string{"account": "983073263818", "environment": "preview", "RDS_REGION": "us-west-2"},
			[]byte("\"us-west-2-catmanagement\""),
		}, // Standard use.
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			result := tokenReplace(tc.format, tc.tokens, logger)
			if !bytes.Equal(result, tc.expected) {
				t.Errorf("Unexpected result: %s\n", result)
			}
		})
	}
}

func TestContainsReference(t *testing.T) {
	testCases := []struct {
		reference v1.ObjectReference
		list      []v1.ObjectReference
		expected  bool
	}{
		{
			objectReferenceA,
			[]v1.ObjectReference{objectReferenceA, objectReferenceB, objectReferenceC},
			true,
		},
		{
			objectReferenceA,
			[]v1.ObjectReference{objectReferenceB, objectReferenceC},
			false,
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			result := containsReference(tc.list, tc.reference)
			if result != tc.expected {
				t.Errorf("Unexpected result for %s in  %s\n", tc.reference, tc.list)
			}
		})
	}
}

func TestAddReferences(t *testing.T) {
	testCases := []struct {
		a        []v1.ObjectReference
		b        []v1.ObjectReference
		expected []v1.ObjectReference
	}{
		{
			[]v1.ObjectReference{objectReferenceB, objectReferenceC},
			[]v1.ObjectReference{objectReferenceA, objectReferenceB, objectReferenceC},
			[]v1.ObjectReference{objectReferenceA, objectReferenceB, objectReferenceC},
		},
		{
			[]v1.ObjectReference{},
			[]v1.ObjectReference{objectReferenceA, objectReferenceB, objectReferenceC},
			[]v1.ObjectReference{objectReferenceA, objectReferenceB, objectReferenceC},
		},
		{
			[]v1.ObjectReference{objectReferenceC},
			[]v1.ObjectReference{objectReferenceA, objectReferenceB},
			[]v1.ObjectReference{objectReferenceA, objectReferenceB, objectReferenceC},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			result := addReferences(tc.a, tc.b)
			if !cmp.Equal(result, tc.expected, cmpopts.SortSlices(lessObjectReference)) {
				t.Errorf("Unexpected result %v expected %v\n", result, tc.expected)
			}
		})
	}
}

func (expected EnvVar) equal(actual EnvVar) bool {
	if actual.Name != expected.Name ||
		actual.Value != expected.Value {
		return false
	}
	return true
}

func (expected SecretRef) equal(actual SecretRef) bool {
	if actual.Name != expected.Name ||
		actual.Key != expected.Key ||
		actual.Path != expected.Path ||
		actual.Value != expected.Value {
		return false
	}
	return true
}

func (expected SecretKeyRef) equal(actual SecretKeyRef) bool {
	if actual.Name != expected.Name ||
		actual.Key != expected.Key ||
		actual.Secret != expected.Secret {
		return false
	}
	return true
}
