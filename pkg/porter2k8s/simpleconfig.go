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
	"strings"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// SimpleEnv contains the env variables, and secrets for a specific region.
type SimpleEnv struct {
	EnvConfigs []SimpleSecretYaml
	Errors     []error
}

// SimpleEntry in a simple config secret yaml.
type SimpleEntry struct {
	K8sObject K8sObjectRef `yaml:"k8s_object"`
	K8sSecret string       `yaml:"k8s_secret"`
	Key       string       `yaml:"key"`
	Name      string       `yaml:"name"`
	Path      string       `yaml:"path"`
	Source    SourceType   `yaml:"source"`
	Value     string       `yaml:"value"`
}

// SimpleSecretYaml contains the format of a Simple secret yaml file.  It is used for unmarshalling json.
type SimpleSecretYaml struct {
	BasePath string        `yaml:"base_path"`
	Includes []string      `yaml:"includes"`
	Type     EntryType     `yaml:"type"`
	Vars     []SimpleEntry `yaml:"vars"`
}

// EntryType determines if the variable is for the container or porter2k8s templating.
type EntryType string

// Possible EntryType values.
const (
	ContainerType EntryType = "container"
	PorterType    EntryType = "porter2k8s" // Old name for porter2ks8 type.
)

// SourceType determines the source of a secret.
type SourceType string

// Secrets can come from vault, K8s secrets such as those created by the ASO, or Kubernetes objects.
const (
	K8sObjectSource SourceType = "object"
	K8sSecretSource SourceType = "kubernetes"
	VaultSource     SourceType = "vault"
)

// K8sObjectRef specifies the field of a Kubernetes object to retrieve.
type K8sObjectRef struct {
	Kind string `yaml:"kind"`
	Name string `yaml:"name"`
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
		switch config.Type {
		case PorterType:
			for _, entry := range config.Vars {
				if entry.Value != "" {
					environment.ClusterSettings[entry.Name] = entry.Value
				} else {
					entry.processSecret(environment, config)
				}
			}
		case ContainerType:
			for _, entry := range config.Vars {
				//log.Debugf("Entry: %+v", entry)
				switch entry.Source {
				case VaultSource:
					// Environment variables have a value, secrets do not.
					if entry.Value != "" {
						newEnvVar := EnvVar{Name: entry.Name, Value: entry.Value}
						environment.Vars = append(environment.Vars, newEnvVar)
					} else {
						entry.processSecret(environment, config)
					}
				case K8sSecretSource:
					// Container secrets injected from Kubernetes Secrets (e.g. from OSB service bindings)
					newSecretRef := SecretKeyRef{Name: entry.Name, Key: entry.Key, Secret: entry.K8sSecret}
					environment.SecretKeyRefs = append(environment.SecretKeyRefs, newSecretRef)
				case K8sObjectSource:
					// Container secrets injected from Kubernetes objects (e.g. from ACK objects)
					//The name of this secret will be `<object_name>-<object_kind` and the key will be the path.
					log.Infof("K8s Object %+v", entry.K8sObject)
					secretName := strings.ToLower(fmt.Sprintf("%s-%s", entry.K8sObject.Name, entry.K8sObject.Kind))
					log.Infof("Secret Name %s", secretName)

					key := removeInvalidCharacters(entry.Path)
					newSecretRef := SecretKeyRef{Name: entry.Name, Key: key, Secret: secretName}
					environment.SecretKeyRefs = append(environment.SecretKeyRefs, newSecretRef)
					// name_kind: [path1, path2,...]
					environment.ObjectRefs[secretName] = append(environment.ObjectRefs[secretName], entry.Path)
				}
			}
		}
	}
}

// UnmarshalYAML sets default Source to "vault".
func (entry *SimpleEntry) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawSimpleEntry SimpleEntry
	raw := rawSimpleEntry{Source: VaultSource}
	if err := unmarshal(&raw); err != nil {
		return err
	}

	*entry = SimpleEntry(raw)
	return nil
}

func (entry *SimpleEntry) processSecret(environment *RegionEnv, config SimpleSecretYaml) {
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
			if len(simpleSecretConfig.Vars) > 0 && (simpleSecretConfig.Type == ContainerType ||
				simpleSecretConfig.Type == PorterType) {
				// Env variables and secrets should override those in the referenced yaml files.
				// This allows setting a value for a specific cluster and allowing all other clusters to share defaults.
				// Remove entries that are overridden or add them to the override map.
				// DO NOT HAVE THE SAME ENTRY IN TWO SIMULTANEOUSLY REFERENCED FILES!!!
				for i := len(simpleSecretConfig.Vars) - 1; i >= 0; i-- {
					overrideEntry := fmt.Sprintf("%s_%s", simpleSecretConfig.Vars[i].Name, simpleSecretConfig.Type)
					if _, ok := overridden[overrideEntry]; ok {
						log.Infof("Env variable %s, overridden\n", simpleSecretConfig.Vars[i].Name)
						simpleSecretConfig.Vars = append(simpleSecretConfig.Vars[:i], simpleSecretConfig.Vars[i+1:]...)
					} else {
						overridden[overrideEntry] = true
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
			if len(referencedFiles) > 0 {
				log.Infof("Recursing on files: %s\n", referencedFiles)
			}
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
