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
	"encoding/base64"
	"fmt"
	"io/ioutil"

	"github.com/ghodss/yaml"
	vaultapi "github.com/hashicorp/vault/api"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ClusterReference contains the path in vault and the name of the context, i.e. an identifier for the cluster.
type ClusterReference struct {
	VaultPath string `json:"path"`
	Context   string `json:"context"`
}

// ClusterConfigs contains all of the information for the Kubernetes client for the clusters.
type ClusterConfigs struct {
	Clusters  []ClusterReference            `json:"clusters"`
	RegionMap map[string]*restclient.Config // map cluster context to config
}

// readReferences reads the cluster config file, which lists the clusters and their respective contexts.
func readReferences(fileName string) (ClusterConfigs, error) {
	var clusterConfigs ClusterConfigs
	fileContent, readErr := ioutil.ReadFile(fileName)
	if readErr != nil {
		return clusterConfigs, readErr
	}
	if err := yaml.Unmarshal(fileContent, &clusterConfigs); err != nil {
		return clusterConfigs, fmt.Errorf("unable to read cluster reference file: %s\n%s", fileName, err)
	}
	return clusterConfigs, nil
}

// fetchConfigs fetches configs from vault.
func (clusterConfigs *ClusterConfigs) fetchConfigs(client *vaultapi.Client) error {
	clusters := make(map[string]*restclient.Config)
	for _, clusterConfig := range clusterConfigs.Clusters {
		vaultSecret, err := client.Logical().Read(clusterConfig.VaultPath)
		if err != nil {
			return err
		}

		encodedConfigValue, ok := vaultSecret.Data["config"]
		if !ok {
			return fmt.Errorf("kubeconfig not found at location %s", clusterConfig.VaultPath)
		}
		encodedConfig, ok := encodedConfigValue.(string)
		if !ok {
			return fmt.Errorf("unable to assert Kubeconfig encoding to string \n%s", encodedConfigValue)
		}

		decodedConfig, err := base64.StdEncoding.DecodeString(encodedConfig)
		if err != nil {
			return err
		}

		config, err := clientcmd.RESTConfigFromKubeConfig(decodedConfig)
		if err != nil {
			return err
		}
		// Update map of configs.
		clusters[clusterConfig.Context] = config
	}
	clusterConfigs.RegionMap = clusters
	return nil
}
