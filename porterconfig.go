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
	"io"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// PorterEnv contains the env variables, and secrets for a specific region.
type PorterEnv struct {
	Errors              []error
	PorterSecretConfigs []PorterSecretYaml
}

// Entry in a porter config secret yaml.
type Entry struct {
	Path  string `yaml:"path"`
	Field string `yaml:"field"`
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// PorterSecretYaml contains the format of a Porter secret yaml file.  It is used for unmarshalling json.
type PorterSecretYaml struct {
	Bundle   string   `yaml:"bundle"`
	Includes []string `yaml:"includes"`
	Group    int      `yaml:"group"`
	BasePath string   `yaml:"base_path"`
	Packages struct {
		// Only concerned with container secrets and env variables.
		Container []Entry `yaml:"container"`
	} `yaml:"packages"`
}

// getEnv implements configReader interface
func (porterEnv *PorterEnv) getEnv(environment *RegionEnv) {
	// Construct paths to secrets.yaml files
	// For example .porter/operation_data/dev/us-east-1/secrets.yaml
	file := []string{fmt.Sprintf(
		"%s/.porter/operation_data/%s/%s/secrets.yaml",
		environment.Cfg.ConfigPath,
		environment.Cfg.Environment,
		environment.Region,
	)}

	porterEnv.getPorterEnv(file, map[string]bool{})
	log.Debugf("%+v\n", porterEnv)
	environment.Errors = porterEnv.Errors
}

// parse reads the PorterEnv and outputs EnvVar and SecretRef types.
func (porterEnv *PorterEnv) parse(environment *RegionEnv) {
	// Segregate secret references from plain environment variables.
	for _, config := range porterEnv.PorterSecretConfigs {
		for _, entry := range config.Packages.Container {
			// Secrets have paths, environment variables do not.
			if entry.Path != "" {
				// Combine base path with path of secret, minus the final key.
				path := filepath.Dir(environment.Cfg.VaultBasePath + config.BasePath + "/" + entry.Path)
				// Only the container type is supported with the porterconfig.
				newSecret := SecretRef{Name: entry.Name, Path: path, Kind: "container"}
				environment.Secrets = append(environment.Secrets, newSecret)
			} else {
				newEnvVar := EnvVar{Name: entry.Name, Value: entry.Value}
				environment.Vars = append(environment.Vars, newEnvVar)
			}
		}
	}
}

// getPorterEnv unmarshals Porter yaml files into environment variables and secrets.
// The files argument is a map of region or cluster name to file path.
// Ex) "us-east-1": ".porter/operation_data/dev/us-east-1/secrets.yaml"
func (porterEnv *PorterEnv) getPorterEnv(files []string, overridden map[string]bool) {
	// Process each yaml file. There will only be one at first, but there may be multiple through recursion.
	for _, file := range files {
		// Create a slice of configs just for this single yaml file. One for each yaml document.
		var referencedFiles []string

		// Open the file.
		fileReader, err := os.Open(file)
		if err != nil {
			porterEnv.Errors = append(porterEnv.Errors, err)
			return
		}

		// Yaml Decoder can handle multiple yaml documents in a single file, delimited by '---'.
		decoder := yaml.NewDecoder(fileReader)
		for {
			// Create a separate Porter config for each yaml document within the file.
			var porterSecretConfig PorterSecretYaml
			err = decoder.Decode(&porterSecretConfig)
			if err == io.EOF {
				// The file has been read to the end.
				break
			} else if err != nil {
				porterEnv.Errors = append(porterEnv.Errors, err)
				// The same yaml document will be re-read infinitely unless we break on error.
				break
			}
			// Filter out yaml documents without container env variables or secrets.
			if len(porterSecretConfig.Packages.Container) > 0 {
				// Env variables and secrets should override those in the referenced yaml files.
				// This allows setting a value for a specific cluster while all other clusters share a default.
				// Remove entries that are overridden or add them to the override map.
				// DO NOT HAVE THE SAME ENTRY IN TWO SIMULTANEOUSLY REFERENCED FILES!!!
				for i := len(porterSecretConfig.Packages.Container) - 1; i >= 0; i-- {
					if _, ok := overridden[porterSecretConfig.Packages.Container[i].Name]; ok {
						log.Infof("Env variable %s, overridden\n", porterSecretConfig.Packages.Container[i].Name)
						porterSecretConfig.Packages.Container = append(
							porterSecretConfig.Packages.Container[:i],
							porterSecretConfig.Packages.Container[i+1:]...,
						)
					} else {
						overridden[porterSecretConfig.Packages.Container[i].Name] = true
					}
				}
				porterEnv.PorterSecretConfigs = append(porterEnv.PorterSecretConfigs, porterSecretConfig)
			}
			if porterSecretConfig.Bundle != "" {
				// Construct referenced file paths.
				// Referenced files are only in the first yaml section of the file,
				// which is the only section with "Bundle".
				for _, relativePath := range porterSecretConfig.Includes {
					// Reference files use relative paths. Filepath knows how to handle them.
					referencedFiles = append(referencedFiles, filepath.Join(filepath.Dir(file), relativePath))
				}
			}
		}

		// Merge porter secret configs referenced in the includes section into the main config for the region.
		// Recurse on referenced yaml files.
		log.Infof("Recursing on files: %s\n", referencedFiles)
		referencedPorterEnv := &PorterEnv{}
		referencedPorterEnv.getPorterEnv(referencedFiles, overridden)
		porterEnv.PorterSecretConfigs = append(
			porterEnv.PorterSecretConfigs,
			referencedPorterEnv.PorterSecretConfigs...,
		)
		porterEnv.Errors = append(porterEnv.Errors, referencedPorterEnv.Errors...)
		log.Debugf("Combined configs: %+v\n", porterEnv.PorterSecretConfigs)
	}
}
