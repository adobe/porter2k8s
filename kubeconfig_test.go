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
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Expected based on content of 'test/clusterconfig.yaml'.
var expectedClusterConfig = ClusterConfigs{
	Clusters: []ClusterReference{
		ClusterReference{
			VaultPath: "secret/test/test/kubeconfig-1.yaml",
			Context:   "us-east-1",
		},
		ClusterReference{
			VaultPath: "30-secret/test_test/k-2#(3.yaml",
			Context:   "other-region",
		},
	},
}

func TestReadReferences(t *testing.T) {
	testCases := []struct {
		filename string
		expected ClusterConfigs
	}{
		{"test/clusterconfig.yaml", expectedClusterConfig},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			pwd, _ := os.Getwd()
			filePath := fmt.Sprintf("%s/%s", pwd, tc.filename)
			readConfig, err := readReferences(filePath)
			if err != nil {
				t.Errorf("Error reading kubeconfig listing: \n%v\n", err)
			}
			equal := cmp.Equal(readConfig, tc.expected, cmp.Comparer(clusterConfigComparer))
			if !equal {
				t.Errorf("Unexpected kubeconfig listing %+v, expected %+v\n", readConfig, tc.expected)
			}
		})
	}
}

func clusterConfigComparer(x, y ClusterConfigs) bool {
	if len(x.Clusters) != len(y.Clusters) {
		return false
	}
	for i, v := range x.Clusters {
		if v.VaultPath != y.Clusters[i].VaultPath || v.Context != y.Clusters[i].Context {
			return false
		}
	}
	return true
}
