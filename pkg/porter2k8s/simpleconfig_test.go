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
	"testing"

	log "github.com/sirupsen/logrus"
)

var simpleTestFiles = []string{"../../test/simpleconfig.yaml"}

var simpleVaultPath = "/secret/vault"

// Expected values from test files in /test."
var simpleExpectedReaderValues = map[string]SimpleEntry{
	"ENV_TEST": {
		Path:  "",
		Name:  "ENV_TEST",
		Value: "environment value",
	},
	"SECRET_TEST": {
		Path: "test_path",
		Name: "SECRET_TEST",
	},
	"BASE_SECRET": {
		Path: "different_path",
		Name: "BASE_SECRET",
	},
	"AWS_ENV": {
		Path:  "",
		Name:  "AWS_ENV",
		Value: "aws_environment_variable",
	},
	"AWS_SECRET": {
		Path: "aws_secret_path",
		Name: "AWS_SECRET",
	},
	"NO_VAL": {
		Name:  "NO_VAL",
		Value: "",
	},
	"NOT_VAULT_KEY": {
		Path: "test_path",
		Name: "NOT_VAULT_KEY",
		Key:  "vault_key",
	},
	"SLASHES": {
		Path: "/another_slash",
		Name: "SLASHES",
	},
	"FROM_K8S": {
		Name:      "FROM_K8S",
		K8sSecret: "k8s-secret",
		Source:    "kubernetes",
		Key:       "k8s_key",
	},
	"FROM_OBJECT": {
		Name: "FROM_OBJECT",
		K8sObject: K8sObjectRef{
			Name: "test_object",
			Kind: "redis",
		},
		Source: "object",
		Path:   "status.url",
	},
	"ENVIRONMENT": {
		Name:  "ENVIRONMENT",
		Value: "gorilla",
	},
	"REGION": {
		Name:  "REGION",
		Value: "westphalia",
	},
	"P2K8S_SECRET": {
		Name: "P2K8S_SECRET",
		Path: "secret_path",
		Key:  "Password",
	},
}

var simpleExpectedRegionEnvVars = map[string]EnvVar{
	"ENV_TEST": {
		Name:  "ENV_TEST",
		Value: "environment value",
	},
	"AWS_ENV": {
		Name:  "AWS_ENV",
		Value: "aws_environment_variable",
	},
}

var simpleExpectedRegionSecretRefs = map[string]SecretRef{
	"SECRET_TEST": {
		Path: simpleVaultPath + "/test_base/test_path",
		Name: "SECRET_TEST",
	},
	"BASE_SECRET": {
		Path: simpleVaultPath + "/different_path",
		Name: "BASE_SECRET",
	},
	"AWS_SECRET": {
		Path: simpleVaultPath + "/aws_secret_path",
		Name: "AWS_SECRET",
	},
	"NOT_VAULT_KEY": {
		Path: simpleVaultPath + "/test_base/test_path",
		Name: "NOT_VAULT_KEY",
		Key:  "vault_key",
	},
	"SLASHES": {
		Path: simpleVaultPath + "/extra_slash/more_slash/another_slash",
		Name: "SLASHES",
	},
	"NO_VAL": {
		Name: "NO_VAL",
		Path: simpleVaultPath + "/test_base",
	},
	"P2K8S_SECRET": {
		Name: "P2K8S_SECRET",
		Path: simpleVaultPath + "/kinda_matters_now/secret_path",
		Key:  "Password",
	},
}

var simpleExpectedRegionSecretKeyRefs = map[string]SecretKeyRef{
	"FROM_K8S": {
		Name:   "FROM_K8S",
		Key:    "k8s_key",
		Secret: "k8s-secret",
	},
	"FROM_OBJECT": {
		Name:   "FROM_OBJECT",
		Key:    "status.url",
		Secret: "test-object-redis",
	},
}

var simpleExpectedClusterSettings = map[string]string{
	"ENVIRONMENT": "gorilla",
	"REGION":      "westphalia",
}

func TestGetSimpleEnv(t *testing.T) {
	testCases := []struct {
		files           []string
		expectedResults map[string]SimpleEntry
	}{
		{
			simpleTestFiles,
			simpleExpectedReaderValues,
		},
	}
	log.SetLevel(log.DebugLevel)
	for _, tc := range testCases {
		t.Run("Test SimpleConfig Reader", func(t *testing.T) {
			counter := 0
			simpleConfig := &SimpleEnv{}
			simpleConfig.getSimpleEnv(tc.files, map[string]bool{}, 0)
			if len(simpleConfig.Errors) != 0 {
				t.Errorf("Unexpected error %s\n", simpleConfig.Errors)
			}
			for _, config := range simpleConfig.EnvConfigs {
				for _, entry := range config.Vars {
					counter++
					expectedEntry, ok := tc.expectedResults[entry.Name]
					if !ok {
						t.Errorf("Unexpected Entry: %+v\n", entry)
					} else if !entry.equal(expectedEntry) {
						t.Errorf("Unexpected Entry values: %+v\nExpected: %+v\n", entry, expectedEntry)
					} else {
						t.Logf("Found %s\n", entry.Name)
					}
				}
			}
			if counter != len(simpleExpectedReaderValues) {
				t.Errorf("Found %d entries, expected %d\n", counter, len(simpleExpectedReaderValues))
			}
		})
	}
}

func TestSimpleParse(t *testing.T) {
	testCases := []struct {
		files              []string
		expectedVars       map[string]EnvVar
		expectedSecrets    map[string]SecretRef
		expectedSecretKeys map[string]SecretKeyRef
		expectedSettings   map[string]string
	}{
		{
			simpleTestFiles,
			simpleExpectedRegionEnvVars,
			simpleExpectedRegionSecretRefs,
			simpleExpectedRegionSecretKeyRefs,
			simpleExpectedClusterSettings,
		},
	}
	log.SetLevel(log.DebugLevel)
	for _, tc := range testCases {
		t.Run("Test SimpleConfig parse", func(t *testing.T) {
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					VaultBasePath: simpleVaultPath,
				},
				ClusterSettings: map[string]string{},
				ObjectRefs:      map[string][]string{},
			}
			simpleConfig := &SimpleEnv{}
			// Use output of first test. Probably not a great practice.
			simpleConfig.getSimpleEnv(tc.files, map[string]bool{}, 0)
			simpleConfig.parse(&regionEnv)

			if len(regionEnv.Vars) != len(tc.expectedVars) {
				t.Errorf("%d env vars found, expected %d", len(regionEnv.Vars), len(tc.expectedVars))
			}
			if len(regionEnv.Secrets) != len(tc.expectedSecrets) {
				t.Errorf("%d secret references found, expected %d", len(regionEnv.Secrets), len(tc.expectedSecrets))
			}
			if len(regionEnv.SecretKeyRefs) != len(tc.expectedSecretKeys) {
				t.Errorf("%d secret key references found, expected %d", len(regionEnv.SecretKeyRefs), len(tc.expectedSecretKeys))
			}
			if len(regionEnv.ClusterSettings) != len(tc.expectedSettings) {
				t.Errorf("%d cluster settings found, expected %d", len(regionEnv.ClusterSettings), len(tc.expectedSettings))
			}

			for _, envVar := range regionEnv.Vars {
				expectedEnvVar, ok := tc.expectedVars[envVar.Name]
				if !ok {
					t.Errorf("Could not find EnvVar in expected list: %+v\n", envVar.Name)
				}
				if !envVar.equal(expectedEnvVar) {
					t.Errorf("Unexpected EnvVar: %+v\nExpected: %+v\n", envVar, expectedEnvVar)
				}
			}
			for _, secret := range regionEnv.Secrets {
				expectedSecret, ok := tc.expectedSecrets[secret.Name]
				if !ok {
					t.Errorf("Could not find Secret in expected list: %+v\n", secret.Name)
				}
				if !secret.equal(expectedSecret) {
					t.Errorf("Unexpected Secret: %+v\nExpected: %+v\n", secret, expectedSecret)
				}
			}
			for _, secretKeyRef := range regionEnv.SecretKeyRefs {
				expectedSecretKeyRef, ok := tc.expectedSecretKeys[secretKeyRef.Name]
				if !ok {
					t.Errorf("Could not find Secret Key Ref in expected list: %+v\n", secretKeyRef.Name)
				}
				if !secretKeyRef.equal(expectedSecretKeyRef) {
					t.Errorf("Unexpected Secret Key Ref: %+v\nExpected: %+v\n", secretKeyRef, expectedSecretKeyRef)
				}
			}
			for setting, value := range regionEnv.ClusterSettings {
				expectedValue, ok := tc.expectedSettings[setting]
				if !ok {
					t.Errorf("Could not find expected Setting: %s", setting)
				}
				if value != expectedValue {
					t.Errorf("Unexpected Setting value: %s Expected: %s", value, expectedValue)
				}
			}
		})
	}
}

func (expected SimpleEntry) equal(actual SimpleEntry) bool {
	if actual.Name != expected.Name ||
		actual.Key != expected.Key ||
		actual.Path != expected.Path ||
		actual.Value != expected.Value {
		return false
	}
	return true
}
