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

var testFiles = []string{"../../test/porterconfig.yaml"}

var vaultPath = "/secret/vault"

// Expected values from test files in /test."
var expectedReaderValues = map[string]Entry{
	"ENV_TEST": {
		Path:  "",
		Name:  "ENV_TEST",
		Value: "environment value",
	},
	"SECRET_TEST": {
		Path:  "test_path",
		Field: "Password",
		Name:  "SECRET_TEST",
	},
	"BASE_SECRET": {
		Path:  "different_path",
		Field: "Cert",
		Name:  "BASE_SECRET",
	},
	"AWS_ENV": {
		Path:  "",
		Name:  "AWS_ENV",
		Value: "aws_environment_variable",
	},
	"AWS_SECRET": {
		Path:  "aws_secret_path",
		Field: "aws_field",
		Name:  "AWS_SECRET",
	},
	"NO_VAL": {
		Name:  "NO_VAL",
		Value: "",
	},
}

var expectedRegionEnvVars = map[string]EnvVar{
	"ENV_TEST": {
		Name:  "ENV_TEST",
		Value: "environment value",
	},
	"AWS_ENV": {
		Name:  "AWS_ENV",
		Value: "aws_environment_variable",
	},
	"NO_VAL": {
		Name:  "NO_VAL",
		Value: "",
	},
}

var expectedRegionSecretRefs = map[string]SecretRef{
	"SECRET_TEST": {
		Path: vaultPath + "/test_base",
		Name: "SECRET_TEST",
	},
	"BASE_SECRET": {
		Path: vaultPath,
		Name: "BASE_SECRET",
	},
	"AWS_SECRET": {
		Path: vaultPath,
		Name: "AWS_SECRET",
	},
}

func TestGetPorterEnv(t *testing.T) {
	testCases := []struct {
		files           []string
		expectedResults map[string]Entry
	}{
		{
			testFiles,
			expectedReaderValues,
		},
	}
	log.SetLevel(log.DebugLevel)
	for _, tc := range testCases {
		t.Run("Test PorterConfig Reader", func(t *testing.T) {
			counter := 0
			porterConfig := &PorterEnv{}
			porterConfig.getPorterEnv(tc.files, map[string]bool{})
			if len(porterConfig.Errors) != 0 {
				t.Errorf("Unexpected error %s\n", porterConfig.Errors)
			}
			for _, config := range porterConfig.PorterSecretConfigs {
				for _, entry := range config.Packages.Container {
					counter++
					expectedEntry, ok := tc.expectedResults[entry.Name]
					if !ok {
						t.Errorf("Could not find entry: %+v\n", entry)
					} else if !entry.equal(expectedEntry) {
						t.Errorf("Unexpected Entry: %+v\nExpected: %+v\n", entry, expectedEntry)
					} else {
						t.Logf("Found %s\n", entry.Name)
					}
				}
			}
			if counter != len(expectedReaderValues) {
				t.Errorf("Found %d entries, expected %d\n", counter, len(expectedReaderValues))
			}
		})
	}
}

func TestParse(t *testing.T) {
	testCases := []struct {
		files           []string
		expectedVars    map[string]EnvVar
		expectedSecrets map[string]SecretRef
	}{
		{
			testFiles,
			expectedRegionEnvVars,
			expectedRegionSecretRefs,
		},
	}
	log.SetLevel(log.DebugLevel)
	for _, tc := range testCases {
		t.Run("Test PorterConfig parse", func(t *testing.T) {
			regionEnv := RegionEnv{
				Cfg: &CmdConfig{
					VaultBasePath: vaultPath,
				},
			}
			porterConfig := &PorterEnv{}
			// Use output of first test. Probably not a great practice.
			porterConfig.getPorterEnv(tc.files, map[string]bool{})
			porterConfig.parse(&regionEnv)

			if len(regionEnv.Vars) != len(tc.expectedVars) {
				t.Errorf("%d env vars found, expected %d", len(regionEnv.Vars), len(tc.expectedVars))
			}
			if len(regionEnv.Secrets) != len(tc.expectedSecrets) {
				t.Errorf("%d secret references found, expected %d", len(regionEnv.Secrets), len(tc.expectedSecrets))
			}

			for _, envVar := range regionEnv.Vars {
				expectedEnvVar, ok := tc.expectedVars[envVar.Name]
				if !ok {
					t.Errorf("Could not find EnvVar in expected list: %+v\n", envVar.Name)
				}
				if !envVar.equal(expectedEnvVar) {
					t.Errorf("Unexpected Entry: %+v\nExpected: %+v\n", envVar, expectedEnvVar)
				}
			}
			for _, secret := range regionEnv.Secrets {
				expectedSecret, ok := tc.expectedSecrets[secret.Name]
				if !ok {
					t.Errorf("Could not find Secret in expected list: %+v\n", secret.Name)
				}
				if !secret.equal(expectedSecret) {
					t.Errorf("Unexpected Entry: %+v\nExpected: %+v\n", secret, expectedSecret)
				}
			}
		})
	}
}

func (expected Entry) equal(actual Entry) bool {
	if actual.Name != expected.Name ||
		actual.Field != expected.Field ||
		actual.Path != expected.Path ||
		actual.Value != expected.Value {
		return false
	}
	return true
}
