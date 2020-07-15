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
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var serviceRunning = v1.Service{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "v1",
		Kind:       "Service",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name:              "running",
		CreationTimestamp: metav1.Time{time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)},
		Labels: map[string]string{
			"app": "testapp",
		},
		Namespace:       "test",
		ResourceVersion: "612488",
		SelfLink:        "/api/v1/namespaces/test/services/testapp",
		UID:             "871f9449-4f4e-11e9-a995-d2a4ff4cc534",
	},
	Spec: v1.ServiceSpec{
		Ports: []v1.ServicePort{
			v1.ServicePort{
				Name:       "http",
				Protocol:   v1.ProtocolTCP,
				TargetPort: intstr.FromInt(8080),
			},
		},
		Selector: map[string]string{
			"app": "testapp",
		},
		SessionAffinity: "None",
		Type:            "ClusterIP",
		ClusterIP:       "127.0.0.1",
	},
	Status: v1.ServiceStatus{},
}

var serviceUpdateNeededValue = v1.Service{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "v1",
		Kind:       "Service",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "no update",
		Labels: map[string]string{
			"app": "testapp",
		},
		Namespace: "test",
	},
	Spec: v1.ServiceSpec{
		Ports: []v1.ServicePort{
			v1.ServicePort{
				Name:       "http",
				Protocol:   v1.ProtocolTCP,
				TargetPort: intstr.FromInt(8083),
			},
		},
		Selector: map[string]string{
			"app": "testapp",
		},
		SessionAffinity: "None",
		Type:            "ClusterIP",
	},
}

var serviceUpdateNeededList = v1.Service{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "v1",
		Kind:       "Service",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "needed list",
		Labels: map[string]string{
			"app": "testapp",
		},
		Namespace: "test",
	},
	Spec: v1.ServiceSpec{
		Ports: []v1.ServicePort{
			v1.ServicePort{
				Name:       "http",
				Protocol:   v1.ProtocolTCP,
				TargetPort: intstr.FromInt(8080),
			},
			v1.ServicePort{
				Name:       "https",
				Protocol:   v1.ProtocolTCP,
				TargetPort: intstr.FromInt(8083),
			},
		},
		Selector: map[string]string{
			"app": "testapp",
		},
		SessionAffinity: "None",
		Type:            "ClusterIP",
	},
}

var serviceUpdateNeededMap = v1.Service{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "v1",
		Kind:       "Service",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "needed map",
		Labels: map[string]string{
			"app": "testapp",
		},
		Namespace: "test",
	},
	Spec: v1.ServiceSpec{
		Ports: []v1.ServicePort{
			v1.ServicePort{
				Name:       "http",
				Protocol:   v1.ProtocolTCP,
				TargetPort: intstr.FromInt(8080),
			},
		},
		Selector: map[string]string{
			"app": "testapp",
			"new": "value",
		},
		SessionAffinity: "None",
		Type:            "ClusterIP",
	},
}

var serviceUpdateNotNeeded = v1.Service{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "v1",
		Kind:       "Service",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "not needed",
		Labels: map[string]string{
			"app": "testapp",
		},
		Namespace: "test",
	},
	Spec: v1.ServiceSpec{
		Ports: []v1.ServicePort{
			v1.ServicePort{
				Name:       "http",
				Protocol:   v1.ProtocolTCP,
				TargetPort: intstr.FromInt(8080),
			},
		},
		Selector: map[string]string{
			"app": "testapp",
		},
		SessionAffinity: "None",
		Type:            "ClusterIP",
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

var serviceAccount1 = v1.ServiceAccount{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "v1",
		Kind:       "ServiceAccount",
	},
	ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{"app": "A"},
		Name:   "A",
	},
	Secrets: []v1.ObjectReference{objectReferenceA, objectReferenceB},
}

var serviceAccount2 = v1.ServiceAccount{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "v1",
		Kind:       "ServiceAccount",
	},
	ObjectMeta: metav1.ObjectMeta{
		Labels:          map[string]string{"app": "A"},
		Name:            "A",
		Namespace:       "test",
		ResourceVersion: "3",
		SelfLink:        "/api/v1/namespaces/test/serviceaccounts/A",
		UID:             "4c6237b9-f6b6-456f-a016-47bcb89a28b7",
		Generation:      5,
	},
	Secrets: []v1.ObjectReference{objectReferenceB, objectReferenceA},
}

var serviceAccount3 = v1.ServiceAccount{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "v1",
		Kind:       "ServiceAccount",
	},
	ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{"app": "A", "security": "enabled"},
		Name:   "A",
	},
	Secrets: []v1.ObjectReference{objectReferenceA, objectReferenceB},
}

func TestMultiDecoder(t *testing.T) {
	testCases := []struct {
		filename     string
		objectNumber int
	}{
		{"test/istio.yaml", 2},
		{"test/secret.yaml", 1},
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

func TestNeedsUpdate(t *testing.T) {
	// Unmarshal HPA from yaml. This is required because it contains unexported types, so we can't just declare them.
	hpaFile := "test/hpa.yaml"
	hpas, readErr := multiDecoder(hpaFile)
	if readErr != nil {
		t.Errorf("Unable to read file %s: %s", hpaFile, readErr)
	}
	testCases := []struct {
		name     string
		running  interface{}
		update   interface{}
		expected bool
	}{
		{"Update not needed", &serviceRunning, &serviceUpdateNotNeeded, false},
		{"Update needed, value", &serviceRunning, &serviceUpdateNeededValue, true},
		{"Update needed, list", &serviceRunning, &serviceUpdateNeededList, true},
		{"Update needed, map", &serviceRunning, &serviceUpdateNeededMap, true},
		{"Update needed, unexported types", hpas[0].Object, hpas[1].Object, true},
		{"Update not needed, unexported types", hpas[0].Object, hpas[0].Object, false},
		{"Update needed, service instances", &serviceInstance1, &serviceInstance2, true},
		{"Update not needed, service accounts", &serviceAccount1, &serviceAccount2, false},
		{"Update needed, service accounts", &serviceAccount1, &serviceAccount3, true},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s", tc.name), func(t *testing.T) {
			result := needsUpdate(tc.running, tc.update)
			if result != tc.expected {
				t.Errorf("Unexpected result expected %t, recieved %t", tc.expected, result)
			}
		})
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
			[]byte("arn:aws:iam::{{.account}}:role/mobilesigcapture-{{.environment}}-{{.region}}"),
		}, // Missing environment. Returns original.
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			result := tokenReplace(tc.format, tc.tokens)
			if !bytes.Equal(result, tc.expected) {
				t.Errorf("Unexpected result: %s\n", result)
			}
		})
	}
}

func TestTokenReplaceString(t *testing.T) {
	testCases := []struct {
		format   string // String with tokens to be replaced.
		tokens   map[string]string
		expected string
	}{
		{
			"arn:aws:iam::{{.account}}:role/mobilesigcapture-{{.environment}}-{{.region}}",
			map[string]string{"account": "983073263818", "environment": "preview", "region": "us-west-2"},
			"arn:aws:iam::983073263818:role/mobilesigcapture-preview-us-west-2",
		}, // Standard use.
		{
			"{{.editor}} is better than {{.os}}.",
			map[string]string{"editor": "Vim", "os": "emacs"},
			"Vim is better than emacs.",
		}, // Random use.
		{
			"arn:aws:iam::{{.account}}:role/mobilesigcapture-{{.environment}}-{{.region}}",
			map[string]string{"account": "983073263818", "region": "us-west-2"},
			"arn:aws:iam::{{.account}}:role/mobilesigcapture-{{.environment}}-{{.region}}",
		}, // Missing environment. Returns original.
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			result := tokenReplaceString(tc.format, tc.tokens)
			if result != tc.expected {
				t.Errorf("Unexpected result: %s\n", result)
			}
			// Also test replaceDeploymentAnnotations use of tokenReplaceString.
			deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{"annotation": tc.format}
			regionEnv := RegionEnv{
				ClusterSettings: tc.tokens,
				Deployment:      &deployment,
			}
			regionEnv.replaceDeploymentAnnotations(regionEnv.Deployment)
			replacedAnnotation := regionEnv.Deployment.Spec.Template.ObjectMeta.Annotations["annotation"]
			if replacedAnnotation != tc.expected {
				t.Errorf("Unexpected result: %s\n", replacedAnnotation)
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
