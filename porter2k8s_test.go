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
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	catalogv1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	contourv1beta1 "github.com/projectcontour/contour/apis/contour/v1beta1"
	projcontour "github.com/projectcontour/contour/apis/projectcontour/v1"
	networkingv1beta1 "istio.io/api/networking/v1beta1"
	istiov1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	autoscaling "k8s.io/api/autoscaling/v2beta2"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	testclient "k8s.io/client-go/kubernetes/fake"
)

var configMapList = v1.ConfigMapList{
	Items: []v1.ConfigMap{
		v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-0123456",
				Labels: map[string]string{
					"app": "testapp",
					"sha": "0123456",
				},
				CreationTimestamp: metav1.Time{time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-1234567",
				Labels: map[string]string{
					"app": "testapp",
					"sha": "1234567",
				},
				CreationTimestamp: metav1.Time{time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-2345678",
				Labels: map[string]string{
					"app": "testapp",
					"sha": "2345678",
				},
				CreationTimestamp: metav1.Time{time.Date(2003, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-3456789",
				Labels: map[string]string{
					"app": "testapp",
					"sha": "3456789",
				},
				CreationTimestamp: metav1.Time{time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-fluent-bit-config",
				Labels: map[string]string{
					"app": "testapp",
				},
				CreationTimestamp: metav1.Time{time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
	},
}

var secretList = v1.SecretList{
	Items: []v1.Secret{
		v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-0123456",
				Labels: map[string]string{
					"app": "testapp",
					"sha": "0123456",
				},
				CreationTimestamp: metav1.Time{time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-1234567",
				Labels: map[string]string{
					"app": "testapp",
					"sha": "1234567",
				},
				CreationTimestamp: metav1.Time{time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-2345678",
				Labels: map[string]string{
					"app": "testapp",
					"sha": "2345678",
				},
				CreationTimestamp: metav1.Time{time.Date(2003, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testapp-3456789",
				Labels: map[string]string{
					"app": "testapp",
					"sha": "3456789",
				},
				CreationTimestamp: metav1.Time{time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
	},
}

var namedSecrets = v1.SecretList{
	Items: []v1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "Service-Named-Secret",
				Labels: map[string]string{
					"app": "testapp",
				},
			},
			StringData: map[string]string{
				"mysecret": "{{.SECRET_VALUE}}",
			},
		},
	},
}

var namedSecretsError = v1.SecretList{
	Items: []v1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "Bad-Example-Secret",
				Labels: map[string]string{
					"app": "testapp",
				},
			},
			StringData: map[string]string{
				"mysecret": "U3RhdGljU2VjcmV0Rm9yYmlkZGVuCg==",
			},
		},
	},
}

var deployment = appsv1.Deployment{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testapp",
	},
	Spec: appsv1.DeploymentSpec{
		Replicas: int32Ref(2),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "testapp",
			},
		},
		Template: v1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "testapp",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					v1.Container{
						Name:  "testapp",
						Image: "test-registry/test-org/test-image",
					},
				},
			},
		},
	},
}

var nilReplicaDeployment = appsv1.Deployment{
	ObjectMeta: metav1.ObjectMeta{
		Name: "nil-replica-testapp",
	},
	Spec: appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "nil-replica-testapp",
			},
		},
		Template: v1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "nil-replica-testapp",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					v1.Container{
						Name:  "nil-replica-testapp",
						Image: "test-registry/test-org/test-image",
					},
				},
			},
		},
	},
}

var job = batchv1.Job{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testapp",
	},
	Spec: batchv1.JobSpec{
		BackoffLimit:          int32Ref(0),
		ActiveDeadlineSeconds: int64Ref(300),
		Template: v1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "testapp",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					v1.Container{
						Name:  "testapp",
						Image: "test-registry/test-org/test-image",
					},
				},
			},
		},
	},
}

var ingress = extensionsv1beta1.Ingress{
	Spec: extensionsv1beta1.IngressSpec{
		TLS: []extensionsv1beta1.IngressTLS{
			extensionsv1beta1.IngressTLS{
				Hosts:      []string{"test-app.micro.test.com", "other-app.micro.test.com"},
				SecretName: "TLS_Secret1",
			},
			extensionsv1beta1.IngressTLS{
				Hosts:      []string{"test-app.micro.test.com", "other-app.micro.test.com"},
				SecretName: "TLS_Secret2",
			},
		},
		Rules: []extensionsv1beta1.IngressRule{
			extensionsv1beta1.IngressRule{
				Host: "test-app.micro.test.com",
			},
			extensionsv1beta1.IngressRule{
				Host: "other-app.micro.test.com",
			},
		},
	},
}

var ingressRouteInternal = contourv1beta1.IngressRoute{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testservice-inressrouteinternal",
		Annotations: map[string]string{
			"kubernetes.io/ingress.class": "contour-internal",
		},
	},
	Spec: contourv1beta1.IngressRouteSpec{
		VirtualHost: &projcontour.VirtualHost{
			Fqdn: "test-app.micro.test.com",
			TLS: &projcontour.TLS{
				SecretName: "heptio-contour/cluster-ssl-int",
			},
		},
	},
}

var ingressRouteExternal = contourv1beta1.IngressRoute{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testservice-ingressrouteexternal",
		Annotations: map[string]string{
			"kubernetes.io/ingress.class": "contour-public",
		},
	},
	Spec: contourv1beta1.IngressRouteSpec{
		VirtualHost: &projcontour.VirtualHost{
			Fqdn: "other-app.micro.test.com",
			TLS: &projcontour.TLS{
				SecretName: "heptio-contour/cluster-ssl-public",
			},
		},
	},
}

var ingressRouteList = contourv1beta1.IngressRouteList{
	Items: []contourv1beta1.IngressRoute{ingressRouteInternal, ingressRouteExternal},
}

var gateway = istiov1beta1.Gateway{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testgw",
	},
	Spec: networkingv1beta1.Gateway{
		Servers: []*networkingv1beta1.Server{
			&networkingv1beta1.Server{
				Hosts: []string{"test-app.micro.test.com", "other-app.micro.test.com"},
				Port: &networkingv1beta1.Port{
					Number:   80,
					Protocol: "HTTP",
					Name:     "http",
				},
			},
			&networkingv1beta1.Server{
				Hosts: []string{"test-app.micro.test.com", "other-app.micro.test.com"},
				Port: &networkingv1beta1.Port{
					Number:   443,
					Protocol: "HTTPS",
					Name:     "https",
				},
				Tls: &networkingv1beta1.Server_TLSOptions{
					Mode:              networkingv1beta1.Server_TLSOptions_SIMPLE,
					PrivateKey:        "/etc/istio/ingressgateway-certs/tls.key",
					ServerCertificate: "/etc/istio/ingressgateway-certs/tls.crt",
				},
			},
		},
	},
}

var virtualService = istiov1beta1.VirtualService{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testvirtualservice",
	},
	Spec: networkingv1beta1.VirtualService{
		Hosts:    []string{"test-app.micro.test.com", "other-app.micro.test.com"},
		Gateways: []string{"testgw"},
		Http: []*networkingv1beta1.HTTPRoute{
			&networkingv1beta1.HTTPRoute{
				Route: []*networkingv1beta1.HTTPRouteDestination{
					&networkingv1beta1.HTTPRouteDestination{
						Destination: &networkingv1beta1.Destination{
							Host: "testapp",
							Port: &networkingv1beta1.PortSelector{
								Number: uint32(80),
							},
						},
						Weight: 100,
					},
				},
			},
		},
	},
}

var horizontalPodAutoscaler = autoscaling.HorizontalPodAutoscaler{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testhorizontalpodautoscaler",
	},
	Spec: autoscaling.HorizontalPodAutoscalerSpec{
		ScaleTargetRef: autoscaling.CrossVersionObjectReference{
			Kind:       "Deployment",
			Name:       "testapp",
			APIVersion: "apps/v1",
		},
		MinReplicas: int32Ref(1),
		MaxReplicas: int32(10),
		Metrics: []autoscaling.MetricSpec{
			autoscaling.MetricSpec{
				Type: autoscaling.ResourceMetricSourceType,
				Resource: &autoscaling.ResourceMetricSource{
					Name: v1.ResourceCPU,
					Target: autoscaling.MetricTarget{
						Type:               autoscaling.UtilizationMetricType,
						AverageUtilization: int32Ref(70),
					},
				},
			},
		},
	},
}

var serviceBinding = catalogv1beta1.ServiceBinding{
	Spec: catalogv1beta1.ServiceBindingSpec{
		SecretName: "test-secret",
		InstanceRef: catalogv1beta1.LocalObjectReference{
			Name: "test-database",
		},
	},
}

var serviceInstance1 = catalogv1beta1.ServiceInstance{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testserviceinstance1",
		Labels: map[string]string{
			"irrelevant": "value",
		},
	},
	Spec: catalogv1beta1.ServiceInstanceSpec{
		ClusterServiceClassRef: &catalogv1beta1.ClusterObjectReference{
			Name: "98374372-8eac-423c-af25-75af9203451e",
		},
		ClusterServicePlanRef: &catalogv1beta1.ClusterObjectReference{
			Name: "9af208c8-b219-1139-9ea4-d6e41afdb762",
		},
		Parameters: &runtime.RawExtension{
			Raw: []byte("parameters"),
		},
	},
}

var serviceInstance2 = catalogv1beta1.ServiceInstance{
	ObjectMeta: metav1.ObjectMeta{
		Name: "testserviceinstance2",
	},
	Spec: catalogv1beta1.ServiceInstanceSpec{
		ClusterServiceClassRef: &catalogv1beta1.ClusterObjectReference{
			Name: "98374372-8eac-423c-af25-75af9203451e",
		},
		ClusterServicePlanRef: &catalogv1beta1.ClusterObjectReference{
			Name: "9af208c8-b219-1139-9ea4-d6e41afdb762",
		},
	},
}

func TestSetReplicas(t *testing.T) {
	bigDeployment := deployment.DeepCopy()
	bigDeployment.Spec.Replicas = int32Ref(10)
	nilDeployment := nilReplicaDeployment.DeepCopy()
	testCases := []struct {
		previousDeployment *appsv1.Deployment
		pendingDeployment  *appsv1.Deployment
		expectedReplicas   int32
	}{
		{&deployment, bigDeployment, 10},
		{bigDeployment, &deployment, 10},
		{bigDeployment, nilDeployment, 10},
	}
	// setReplicas should always set the number of replicas to the larger value.
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Number of replicas %d", tc.expectedReplicas), func(t *testing.T) {
			var finalReplicaCount int32
			setReplicas(tc.pendingDeployment, tc.previousDeployment)
			if tc.pendingDeployment.Spec.Replicas != nil {
				finalReplicaCount = *tc.pendingDeployment.Spec.Replicas
			}
			if finalReplicaCount != tc.expectedReplicas {
				t.Errorf("Unexpected Number of Replicas %d\n", finalReplicaCount)
			}
		})
	}
}

func TestCreateConfigMap(t *testing.T) {
	testCases := []struct {
		deploymentSha string
	}{
		{"267c65f"}, // Try to create ConfigMap with sha that does not exist.
		{"0123456"}, // Try to create ConfigMap with sha that does exist.	}
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			clientset := testclient.NewSimpleClientset(&configMapList)
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					SHA: tc.deploymentSha,
				},
				Clientset: clientset,
			}
			configMapInterface := clientset.CoreV1().ConfigMaps("")
			configMapFound := false
			listOptions := metav1.ListOptions{
				LabelSelector: "app=testapp",
			}

			createSucceeded := regionEnv.createConfigMap("testapp")
			if !createSucceeded {
				t.Errorf("Unexpected Config Map creation failure\n%s", regionEnv.Errors)
			}
			remainingConfigMaps, _ := configMapInterface.List(listOptions)
			// Ensure ConfigMap exists
			for _, remainingConfigMap := range remainingConfigMaps.Items {
				if strings.Contains(remainingConfigMap.ObjectMeta.Name, tc.deploymentSha) {
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
	}{
		{4, "", "testapp", 4, ""},                // Nothing deleted.
		{3, "", "testapp", 3, "testapp-0123456"}, // Oldest CM deleted.
		{3, "0123456", "testapp", 4, ""},         // Nothing deleted due to protected SHA.
		{3, "", "otherapp", 5, ""},               // Nothing deleted due to other app name SHA.
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			clientset := testclient.NewSimpleClientset(&configMapList)
			configMapInterface := clientset.CoreV1().ConfigMaps("")
			listOptions := metav1.ListOptions{
				LabelSelector: "app=testapp",
			}
			protectedShas := map[string]bool{
				tc.protectedSha: true,
			}
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					MaxConfigMaps: tc.maxConfigMaps,
				},
				Clientset: clientset,
			}

			deleteSuccessful := regionEnv.deleteOldConfigMaps(tc.appName, protectedShas)
			if !deleteSuccessful {
				t.Error(regionEnv.Errors)
			}
			// Check if the correct number of configmaps are returned
			remainingConfigMaps, _ := configMapInterface.List(listOptions)
			remainingConfigMapsCount := len(remainingConfigMaps.Items)
			if remainingConfigMapsCount != tc.expectedConfigMapCount {
				t.Errorf(
					"Incorrect number of ConfigMaps remaining %d, expected %d.\n",
					remainingConfigMapsCount,
					tc.expectedConfigMapCount)
			}
			// Ensure the correct configmap was deleted
			for _, remainingConfigMap := range remainingConfigMaps.Items {
				if tc.expectedDeletedConfigMap == remainingConfigMap.ObjectMeta.Name {
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
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			clientset := testclient.NewSimpleClientset(&secretList)
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					SHA: tc.deploymentSha,
				},
				Clientset: clientset,
			}
			secretInterface := clientset.CoreV1().Secrets("")
			secretFound := false
			listOptions := metav1.ListOptions{
				LabelSelector: "app=testapp",
			}

			createSucceeded := regionEnv.createSecret("testapp")
			if !createSucceeded {
				t.Errorf("Unexpected Config Map creation failure\n%s", regionEnv.Errors)
			}
			remainingSecrets, _ := secretInterface.List(listOptions)
			// Ensure Secret exists
			for _, remainingSecret := range remainingSecrets.Items {
				if strings.Contains(remainingSecret.ObjectMeta.Name, tc.deploymentSha) {
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
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			clientset := testclient.NewSimpleClientset(&secretList)
			secretInterface := clientset.CoreV1().Secrets("")
			listOptions := metav1.ListOptions{
				LabelSelector: "app=testapp",
			}
			protectedShas := map[string]bool{
				tc.protectedSha: true,
			}
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					MaxConfigMaps: tc.maxSecrets,
				},
				Clientset: clientset,
			}

			deleteSuccessful := regionEnv.deleteOldSecrets(tc.appName, protectedShas)
			if !deleteSuccessful {
				t.Error(regionEnv.Errors)
			}
			if err != nil {
				t.Error(err.Error())
			}
			// Check if the correct number of secrets are returned
			remainingSecrets, _ := secretInterface.List(listOptions)
			remainingSecretsCount := len(remainingSecrets.Items)
			if remainingSecretsCount != tc.expectedSecretCount {
				t.Errorf(
					"Incorrect number of Secrets remaining %d, expected %d.\n",
					remainingSecretsCount,
					tc.expectedSecretCount)
			}
			// Ensure the correct secret was deleted
			for _, remainingSecret := range remainingSecrets.Items {
				if tc.expectedDeletedSecret == remainingSecret.ObjectMeta.Name {
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
		data     map[string]string
		expected map[string]string
	}{
		{
			"porter2k8s",
			map[string]string{},
			map[string]string{"Test": "TEST"},
			map[string]string{"Test": "TEST"},
		},
		{
			"porter2k8s",
			map[string]string{"Test": "other"},
			map[string]string{"Test": "TEST"},
			map[string]string{"Test": "other"},
		},
		{
			"notPorter2k8s",
			map[string]string{},
			map[string]string{"Test": "TEST"},
			map[string]string{},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			porter2k8sCM := v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tc.name,
					Namespace: "default",
				},
				Data: tc.data,
			}
			clientset := testclient.NewSimpleClientset(&porter2k8sCM)
			regionEnv := RegionEnv{
				Clientset: clientset,
				Cfg: &CmdConfig{
					Namespace: "default",
				},
				ClusterSettings: tc.original,
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
			testVirtualService := virtualService.DeepCopy()
			testVirtualService.Spec.Gateways = tc.originalGateways
			regionEnv.VirtualService = testVirtualService
			if tc.replacementGateways != "" {
				regionEnv.ClusterSettings = map[string]string{"GATEWAYS": tc.replacementGateways}
			}

			// Run updateRegistry.
			regionEnv.updateGateway()
			for i, result := range testVirtualService.Spec.Gateways {
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
			testDeployment := deployment.DeepCopy()
			testDeployment.ObjectMeta.Name = tc.name
			testDeployment.Spec.Template.Spec.Containers[0].Image = tc.image
			testDeployment.Spec.Template.Spec.Containers[0].Name = tc.name
			regionEnv.Deployment = testDeployment
			if tc.registry != "" {
				regionEnv.ClusterSettings = map[string]string{"REGISTRY": tc.registry}
			}

			// Run updateRegistry.
			regionEnv.updateRegistry(regionEnv.Deployment)
			result := testDeployment.Spec.Template.Spec.Containers[0].Image
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
			testIngress := ingress.DeepCopy()
			testGateway := gateway.DeepCopy()
			testVirtualService := virtualService.DeepCopy()
			testIngressRouteList := ingressRouteList.DeepCopy()
			regionEnv.Region = "theBestRegion"
			regionEnv.Ingress = testIngress
			regionEnv.Gateway = testGateway
			regionEnv.VirtualService = testVirtualService
			regionEnv.IngressRouteList = testIngressRouteList
			if tc.newDomain != "" {
				regionEnv.ClusterSettings = map[string]string{"DOMAIN": tc.newDomain}
			}

			// Run updateDomainName.
			regionEnv.updateDomainName()
			// Check rules.
			for _, ingressRule := range regionEnv.Ingress.Spec.Rules {
				if !stringInExpected(ingressRule.Host, tc.expected) {
					t.Errorf("Incorrect host name %s, expected domain(s) %s.\n", ingressRule.Host, tc.expected)
				}
			}
			// Check Ingress TLS.
			for _, ingressTLS := range regionEnv.Ingress.Spec.TLS {
				hosts := ingressTLS.Hosts
				for _, host := range hosts {
					if !stringInExpected(host, tc.expected) {
						t.Errorf("Incorrect host name %s, expected domain(s) %s.\n", host, tc.expected)
					}
				}
			}
			// Check Gateway.
			for _, server := range regionEnv.Gateway.Spec.Servers {
				for _, host := range server.Hosts {
					if !stringInExpected(host, tc.expected) {
						t.Errorf("Incorrect host name %s, expected domain(s) %s.\n", host, tc.expected)
					}
				}
			}
			// Check Virtual Service.
			for _, host := range regionEnv.VirtualService.Spec.Hosts {
				if !stringInExpected(host, tc.expected) {
					t.Errorf("Incorrect host name %s, expected domain(s) %s.\n", host, tc.expected)
				}
			}
			// Check Virtual Host in IngressRoute(s)
			for _, ingressroute := range regionEnv.IngressRouteList.Items {
				if !stringInExpected(ingressroute.Spec.VirtualHost.Fqdn, tc.expected) {
					t.Errorf(
						"Incorrect host name %s, expected domain(s) %s.\n",
						ingressroute.Spec.VirtualHost.Fqdn,
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
			original.ObjectMeta.ResourceVersion = tc.originalHPAVersion
			copy := original.DeepCopy()
			copy.ObjectMeta.ResourceVersion = tc.copyHPAVersion
			if original.ObjectMeta.ResourceVersion != tc.expected {
				t.Errorf("HPA DeepCopy failed. Expected %s, but received %s\n",
					tc.expected,
					original.ObjectMeta.ResourceVersion,
				)
			}
		})
	}
}

func TestUpdateHPAMinimum(t *testing.T) {
	testCases := []struct {
		clustermin  string
		hPAMin      *int32
		hPAMax      int32
		expectedMin int32
		expectedMax int32
	}{
		{"1", int32Ref(1), int32(10), int32(1), int32(10)},
		{"2", int32Ref(1), int32(10), int32(2), int32(10)},
		{"3", int32Ref(1), int32(2), int32(3), int32(3)},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Setup.
			var regionEnv RegionEnv
			regionEnv.ClusterSettings = map[string]string{"HPAMIN": tc.clustermin}
			horizontalPodAutoscaler.Spec.MinReplicas = tc.hPAMin
			horizontalPodAutoscaler.Spec.MaxReplicas = tc.hPAMax
			regionEnv.HPAutoscaler = &horizontalPodAutoscaler
			regionEnv.updateHPAMinimum()
			if *regionEnv.HPAutoscaler.Spec.MinReplicas != tc.expectedMin {
				t.Errorf(
					"Incorrect minimum replica number %d, expected %d.\n",
					*regionEnv.HPAutoscaler.Spec.MinReplicas, tc.expectedMin,
				)
			}
			if regionEnv.HPAutoscaler.Spec.MaxReplicas != tc.expectedMax {
				t.Errorf(
					"Incorrect maximum replica number %d, expected %d.\n",
					regionEnv.HPAutoscaler.Spec.MaxReplicas,
					tc.expectedMax,
				)
			}
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
				testDeployment.Spec.Template.Spec.Containers[0].LivenessProbe = &livenessProbe
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
			testDeployment := deployment.DeepCopy()
			testDeployment.Spec.Template.Spec.Containers[0].Image = tc.image
			err := prepareDeployment(testDeployment, tc.sha)
			if err != nil {
				t.Errorf("Unexpected error: %s\n", err)
			}
			imageName := testDeployment.Spec.Template.Spec.Containers[0].Image
			if imageName != tc.expected {
				t.Errorf("Incorrect image name %s, expected %s.\n", imageName, tc.expected)
			}
		})
	}
}

func TestParentObject(t *testing.T) {
	deploymentCfg := KubeObjects{Deployment: &deployment}
	if parent := deploymentCfg.parentObject(); parent != deploymentCfg.Deployment {
		t.Fatalf("parentObject() returned wrong value %v", parent)
	}

	jobCfg := KubeObjects{Job: &job}
	if parent := jobCfg.parentObject(); parent != jobCfg.Job {
		t.Fatalf("parentObject() returned wrong value %v", parent)
	}
}

func TestFindContainer(t *testing.T) {
	container := findContainer(&deployment)
	if container == nil || container.Name != deployment.Name {
		t.Fatalf("findContainer() returned wrong value %v", container)
	}

	container = findContainer(&job)
	if container == nil || container.Name != job.Name {
		t.Fatalf("findContainer() returned wrong value %v", container)
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
			regionEnv := RegionEnv{
				Deployment:    deployment.DeepCopy(),
				SecretKeyRefs: []SecretKeyRef{tc.secretKeyRef},
			}
			err := regionEnv.injectSecretKeyRefs(regionEnv.Deployment)
			if err != nil {
				t.Errorf("Unexpected error: %s\n", err)
			}
			// Only 1 container and 1 environment variable.
			envVar := regionEnv.Deployment.Spec.Template.Spec.Containers[0].Env[0]
			if envVar.Name != tc.secretKeyRef.Name ||
				envVar.ValueFrom.SecretKeyRef.Name != tc.secretKeyRef.Secret ||
				envVar.ValueFrom.SecretKeyRef.Key != tc.secretKeyRef.Key {

				t.Errorf("Incorrect secretname injected into deployment %+v", envVar)
			}
		})
	}
}

func TestManualSidecarInjectionRequired(t *testing.T) {
	testCases := []struct {
		namespace       string
		clusterSettings map[string]string
		expected        bool
	}{
		{
			"istio-autoinject-on",
			map[string]string{"ISTIO": "true"},
			false,
		},
		{
			"istio-autoinject-off",
			map[string]string{"ISTIO": "true"},
			true,
		},
		{
			"istio-autoinject-on",
			map[string]string{},
			false,
		},
		{
			"istio-autoinject-on",
			map[string]string{},
			false,
		},
	}
	// Setup
	istioAIOffNamespace := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "istio-autoinject-off",
			CreationTimestamp: metav1.Time{time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	istioAIOnNamespace := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "istio-autoinject-on",
			Labels: map[string]string{
				"istio-injection": "enabled",
			},
			CreationTimestamp: metav1.Time{time.Date(2002, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	clientset := testclient.NewSimpleClientset(&istioAIOnNamespace, &istioAIOffNamespace)
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					Namespace: tc.namespace,
				},
				Clientset:       clientset,
				ClusterSettings: tc.clusterSettings,
			}
			result := regionEnv.manualSidecarInjectionRequired()
			if result != tc.expected {
				t.Errorf("Incorrect return value %+v, expected %+v.\n", result, tc.expected)
			}
		})
	}
}

func TestReplaceServiceInstanceParameters(t *testing.T) {
	testCases := []struct {
		parameters      []byte
		clusterSettings map[string]string
		expected        []byte
	}{
		{
			[]byte(`subnetID: "/subs/{{.ACCOUNT}}/rg/{{.BASERESOURCEGROUP}}/{{.VNET}}/{{.VNET}}-Database-subnet"`),
			map[string]string{"ACCOUNT": "abcdefg", "BASERESOURCEGROUP": "base-rg", "VNET": "test-vnet"},
			[]byte(`subnetID: "/subs/abcdefg/rg/base-rg/test-vnet/test-vnet-Database-subnet"`),
		},
	}
	cloud := "cloudprovider"
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			testServiceInstance := serviceInstance1.DeepCopy()
			testServiceInstance.Spec.Parameters.Raw = tc.parameters
			serviceInstances := make(map[string][]*catalogv1beta1.ServiceInstance)
			serviceInstances[cloud] = []*catalogv1beta1.ServiceInstance{
				testServiceInstance,
			}
			regionEnv := &RegionEnv{
				ClusterSettings:  tc.clusterSettings,
				ServiceInstances: serviceInstances,
			}
			regionEnv.replaceServiceInstanceParameters(cloud)
			result := regionEnv.ServiceInstances[cloud][0].Spec.Parameters.Raw
			if !bytes.Equal(result, tc.expected) {
				t.Errorf("Incorrect parameter replacement %s, expected %s.\n", result, tc.expected)
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
		name                 string
		deployName           string
		hPAName              string
		hPATarget            string
		serviceInstanceName1 string
		serviceInstanceName2 string
		serviceName          string
		errorMessage         string
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
		},
		{
			"Wrong HPA deployment reference",
			"testservice",
			"testserviceHPA",
			"otherservice",
			"testserviceRDS",
			"testserviceSQL",
			"testservice",
			"HPAutoscaler references",
		},
		{
			"Wrong HPA name",
			"testservice",
			"otherserviceHPA",
			"testservice",
			"testserviceRDS",
			"testserviceSQL",
			"testservice",
			"does not contain service name",
		},
		{
			"Wrong Service Instance name",
			"testservice",
			"testserviceHPA",
			"testservice",
			"testserviceRDS",
			"otherserviceSQL",
			"testservice",
			"does not contain service name",
		},
		{
			"Worker without Service Object and wrong HPA name",
			"testworker",
			"testserviceHPA",
			"testworker",
			"",
			"",
			"",
			"does not contain service name",
		},
		{
			"Worker without Service Object",
			"testworker",
			"testworkerHPA",
			"testworker",
			"",
			"",
			"",
			"",
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			serviceInstances := make(map[string][]*catalogv1beta1.ServiceInstance)
			serviceInstance1.ObjectMeta.Name = tc.serviceInstanceName1
			serviceInstance2.ObjectMeta.Name = tc.serviceInstanceName2
			serviceInstances["cloudprovider"] = []*catalogv1beta1.ServiceInstance{
				serviceInstance1.DeepCopy(),
				serviceInstance2.DeepCopy(),
			}

			serviceRunning.ObjectMeta.Name = tc.serviceName
			horizontalPodAutoscaler.ObjectMeta.Name = tc.hPAName
			horizontalPodAutoscaler.Spec.ScaleTargetRef.Name = tc.hPATarget
			deployment.ObjectMeta.Name = tc.deployName
			kubeObjects := KubeObjects{
				Deployment:       deployment.DeepCopy(),
				HPAutoscaler:     horizontalPodAutoscaler.DeepCopy(),
				Service:          serviceRunning.DeepCopy(),
				ServiceInstances: serviceInstances,
				IngressRouteList: ingressRouteList.DeepCopy(),
			}
			err := kubeObjects.validateAndTag()

			if err != nil {
				if tc.errorMessage == "" {
					t.Errorf("Unexpected error message: %s, none expected.", err)
				} else if !strings.Contains(err.Error(), tc.errorMessage) {
					t.Errorf("Unexpected error message: %s, expected %s", err, tc.errorMessage)
				}
			} else {
				// Verify all objects have been tagged.
				if kubeObjects.HPAutoscaler.ObjectMeta.Labels["app"] != tc.serviceName ||
					kubeObjects.ServiceInstances["cloudprovider"][0].ObjectMeta.Labels["app"] != tc.serviceName ||
					kubeObjects.ServiceInstances["cloudprovider"][1].ObjectMeta.Labels["app"] != tc.serviceName {
					t.Error("KubeObjects are not tagged with service name")
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
		secretList      v1.SecretList
		clusterSettings map[string]string
		expected        []byte
		errorExpected   bool
	}{
		{
			namedSecrets,
			map[string]string{"SECRET_VALUE": "werewolvesnotswearwolves"},
			[]byte(`werewolvesnotswearwolves`),
			false,
		},
		{
			namedSecretsError,
			map[string]string{"SECRET_VALUE": "thisvaluedoesnotmatter"},
			[]byte(`U3RhdGljU2VjcmV0Rm9yYmlkZGVuCg==`),
			true,
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			testNamedSecretList := tc.secretList.DeepCopy()
			regionEnv := &RegionEnv{
				ClusterSettings: tc.clusterSettings,
				NamedSecrets:    testNamedSecretList,
			}
			secretEntry := "mysecret"
			regionEnv.replaceNamedSecretData()
			for _, namedSecret := range regionEnv.NamedSecrets.Items {
				if !bytes.Equal([]byte(namedSecret.StringData[secretEntry]), tc.expected) {
					t.Errorf("Incorrect value replacement %s, expected %s. \n",
						string(namedSecret.StringData[secretEntry]),
						string(tc.expected))
				} else if len(regionEnv.Errors) > 0 && tc.errorExpected != true {
					t.Errorf("Unexpected error encountered with key: %s, value: %s\n",
						string(secretEntry),
						string(namedSecret.StringData[secretEntry]))
				}
			}
		})
	}
}
