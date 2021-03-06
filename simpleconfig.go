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
	"regexp"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// SimpleEnv contains the env variables, and secrets for a specific region.
type SimpleEnv struct {
	Errors     []error
	EnvConfigs []SimpleSecretYaml
}

// SimpleEntry in a simple config secret yaml.
type SimpleEntry struct {
	Path      string `yaml:"path"`
	Name      string `yaml:"name"`
	Value     string `yaml:"value"`
	Key       string `yaml:"key"`
	Source    string `yaml:"source"`
	K8sSecret string `yaml:"k8s_secret"`
}

// SimpleSecretYaml contains the format of a Simple secret yaml file.  It is used for unmarshalling json.
type SimpleSecretYaml struct {
	Includes []string      `yaml:"includes"`
	BasePath string        `yaml:"base_path"`
	Type     string        `yaml:"type"`
	Vars     []SimpleEntry `yaml:"vars"`
}

// getEnv implements configReader interface
func (simpleEnv *SimpleEnv) getEnv(environment *RegionEnv) {
	// Construct paths to environment.yaml files
	// For example environment/dev/us-east-1/environment.yaml
	file := []string{fmt.Sprintf("%s/environment/%s/%s/environment.yaml",
		environment.Cfg.ConfigPath,
		environment.Cfg.Environment,
		environment.Region)}

	simpleEnv.getSimpleEnv(file, map[string]bool{}, 0)
	log.Debugf("%+v\n", simpleEnv)
	environment.Errors = simpleEnv.Errors
}

// parse reads the SimpleEnv and outputs EnvVar and SecretRef types.
func (simpleEnv *SimpleEnv) parse(environment *RegionEnv) {
	// Segregate secret references from plain environment variables.
	for _, config := range simpleEnv.EnvConfigs {
		for _, entry := range config.Vars {
			// Only 2 types are porter2k8s and container. Both can contain k/v pairs or secrets
			// Environment variables have a value, secrets do not.
			if entry.Value != "" {
				if config.Type == "porter2k8s" {
					environment.ClusterSettings[entry.Name] = entry.Value
				} else if config.Type == "container" {
					newEnvVar := EnvVar{Name: entry.Name, Value: entry.Value}
					environment.Vars = append(environment.Vars, newEnvVar)
				}
				// Container secrets injected from Kubernetes Secret objects (e.g., from OSB service bindings)
			} else if entry.Source == "kubernetes" {
				newSecretRef := SecretKeyRef{Name: entry.Name, Key: entry.Key, Secret: entry.K8sSecret}
				environment.SecretKeyRefs = append(environment.SecretKeyRefs, newSecretRef)
			} else {
				// Secrets.
				// Combine base path with path of secret.
				path := environment.Cfg.VaultBasePath + "/" + config.BasePath
				if entry.Path != "" {
					path = path + "/" + entry.Path
				}
				// Remove duplicate slashes.
				regex, _ := regexp.Compile(`//+`)
				path = regex.ReplaceAllString(path, "/")
				newSecret := SecretRef{Name: entry.Name, Path: path, Key: entry.Key, Kind: config.Type}
				environment.Secrets = append(environment.Secrets, newSecret)
			}
		}
	}
}

// getSimpleEnv unmarshals environment yaml files into environment variables and secrets.
// The files argument is a map of region or cluster name to file path.
// Ex) "us-east-1": "environment/dev/us-east-1/environment.yaml"
func (simpleEnv *SimpleEnv) getSimpleEnv(files []string, overridden map[string]bool, depth int) {
	// Recursion depth.
	maxDepth := 10
	// Process each yaml file. There will only be one at first, but there may be multiple through recursion.
	for _, file := range files {
		// Create a slice of configs just for this single yaml file. One for each yaml document.
		var referencedFiles []string

		// Open the file.
		fileReader, err := os.Open(file)
		if err != nil {
			simpleEnv.Errors = append(simpleEnv.Errors, err)
			return
		}

		// Yaml Decoder can handle multiple yaml documents in a single file, delimited by '---'.
		decoder := yaml.NewDecoder(fileReader)
		for {
			// Create a separate config for each yaml document within the file.
			var simpleSecretConfig SimpleSecretYaml
			err = decoder.Decode(&simpleSecretConfig)
			if err == io.EOF {
				// The file has been read to the end.
				break
			} else if err != nil {
				simpleEnv.Errors = append(simpleEnv.Errors, err)
				// The same yaml document will be re-read infinitely unless we break on error.
				break
			}
			// Filter out yaml documents without container env variables or secrets.
			// We are only interested in container secrets.
			if len(simpleSecretConfig.Vars) > 0 &&
				(simpleSecretConfig.Type == "container" || simpleSecretConfig.Type == "porter2k8s") {
				// Env variables and secrets should override those in the referenced yaml files.
				// This allows setting a value for a specific cluster and allowing all other clusters to share defaults.
				// Remove entries that are overridden or add them to the override map.
				// DO NOT HAVE THE SAME ENTRY IN TWO SIMULTANEOUSLY REFERENCED FILES!!!
				for i := len(simpleSecretConfig.Vars) - 1; i >= 0; i-- {
					if _, ok := overridden[simpleSecretConfig.Vars[i].Name]; ok {
						log.Infof("Env variable %s, overridden\n", simpleSecretConfig.Vars[i].Name)
						simpleSecretConfig.Vars = append(simpleSecretConfig.Vars[:i], simpleSecretConfig.Vars[i+1:]...)
					} else {
						overridden[simpleSecretConfig.Vars[i].Name] = true
					}
				}
				simpleEnv.EnvConfigs = append(simpleEnv.EnvConfigs, simpleSecretConfig)
			}
			if len(simpleSecretConfig.Includes) != 0 {
				// Construct referenced file paths.
				// Referenced files are only in the first yaml section of the file, the only section with "includes".
				for _, relativePath := range simpleSecretConfig.Includes {
					// Reference files use relative paths. Filepath knows how to handle them.
					referencedFiles = append(referencedFiles, filepath.Join(filepath.Dir(file), relativePath))
				}
			}
		}

		// Merge simple secret configs referenced in the includes section into the main config for the region.
		// Recurse on referenced yaml files.
		if depth < maxDepth {
			depth++
			log.Infof("Recursing on files: %s\n", referencedFiles)
			referencedSimpleEnv := &SimpleEnv{}
			referencedSimpleEnv.getSimpleEnv(referencedFiles, overridden, depth)
			simpleEnv.EnvConfigs = append(simpleEnv.EnvConfigs, referencedSimpleEnv.EnvConfigs...)
			simpleEnv.Errors = append(simpleEnv.Errors, referencedSimpleEnv.Errors...)
			log.Debugf("Combined configs: %+v\n", simpleEnv.EnvConfigs)
		} else {
			log.Errorf("Reached Maximum Recursion Depth: %d", maxDepth)
		}
	}
}
