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
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/google/go-cmp/cmp"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeyaml "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Decoded Kubernetes Object
type Decoded struct {
	Object runtime.Object
	GVK    string
}

// BoolRef returns a reference to the boolean parameter.
func boolRef(b bool) *bool {
	return &b
}

// Int64Ref returns a reference to the int parameter.
func int64Ref(i interface{}) *int64 {
	var i64 int64
	switch asserted := i.(type) {
	case int:
		i64 = int64(asserted)
	case int32:
		i64 = int64(asserted)
	case string:
		integer, err := strconv.Atoi(asserted)
		if err != nil {
			log.Errorf("Unable to convert %v to int64 reference\n", i)
		} else {
			i64 = int64(integer)
		}
	default:
		log.Errorf("Unable to convert %v to int64 reference. Argument must be string or int.\n", i)
	}
	return &i64
}

// Int32Ref returns a reference to the int parameter.
func int32Ref(i interface{}) *int32 {
	var i32 int32
	switch asserted := i.(type) {
	case int:
		i32 = int32(asserted)
	case int64:
		i32 = int32(asserted)
	case string:
		integer, err := strconv.Atoi(asserted)
		if err != nil {
			log.Errorf("Unable to convert %v to int32 reference\n", i)
		} else {
			i32 = int32(integer)
		}
	default:
		log.Errorf("Unable to convert %v to int32 reference. Argument must be string or int.\n", i)
	}
	return &i32
}

// SetFromEnvStr sets parameter from environment, if available.
func setFromEnvStr(envVariable string, defaultValue string) string {
	if value := os.Getenv(envVariable); value != "" {
		return value
	}
	return defaultValue
}

// SetFromEnvStr sets parameter from environment, if available.
func setFromEnvBool(envVariable string) bool {
	if value := os.Getenv(envVariable); value != "" {
		return true
	}
	return false
}

// SetFromEnvInt sets parameter from environment, if available.
func setFromEnvInt(envVariable string, defaultValue int) int {
	if value := os.Getenv(envVariable); value != "" {
		ret, err := strconv.Atoi(value)
		if err == nil {
			return ret
		}
		log.Errorf("Unexpected %s value: %s, using %d\n", envVariable, value, defaultValue)
	}
	return defaultValue
}

func setIfZeroInt32(value, defaultValue int32) int32 {
	if value == 0 {
		return defaultValue
	}
	return value
}

// tokenReplace replaces a []byte template with values from a dict.
func tokenReplace(format []byte, args map[string]string, logger *log.Entry) []byte {
	var message bytes.Buffer
	strFormat := string(format)
	// Sprig functions may create maps or lists. If we don't remove the single quotes those will just be strings.
	regex := regexp.MustCompile(`'(.*{{.*}}.*)'`)
	strFormat = regex.ReplaceAllString(strFormat, "$1")

	tmpl, err := template.New("tokens").Funcs(sprig.TxtFuncMap()).Parse(strFormat)

	if err != nil {
		logger.Errorf("Error creating sprig template for replacing tokens: %s", err)
		return format
	}
	// Make the formatter enter a zero value if the token is not found.
	tmpl.Option("missingkey=zero")

	err = tmpl.Execute(&message, args)
	if err != nil {
		logger.Warnf("Token missing or is not applicable for this namespace, cloud, or region: %s", err)
		return format
	}
	return message.Bytes()
}

// multiDecoder reads multiple Kubernetes objects from the same file.
func multiDecoder(filename string) (objects []Decoded, err error) {
	data, readErr := ioutil.ReadFile(filename)
	if readErr != nil {
		return objects, readErr
	}

	r := ioutil.NopCloser(bytes.NewReader(data))
	decoder := yaml.NewDocumentDecoder(r)
	unstructuredDecoder := runtimeyaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	var chunk []byte
	for {
		chunk = make([]byte, len(data))
		_, err = decoder.Read(chunk)
		if err != nil {
			break
		}
		chunk = bytes.Trim(chunk, "\x00")

		obj, gvk, err := unstructuredDecoder.Decode(chunk, nil, nil)
		if err != nil {
			log.Errorf("Unstructured Decoding Error: %v", err)
			return nil, err
		}
		log.Infof("gvk: %+v", gvk)
		objects = append(objects, Decoded{Object: obj, GVK: gvk.String()})
	}
	// No need to pass EOF error to caller.
	if err == io.EOF {
		err = nil
	}
	return objects, err
}

// UnstructuredHeap is a heap in which each node is the oldest configmap in its subtree.
type UnstructuredHeap []unstructured.Unstructured

func (h UnstructuredHeap) Len() int      { return len(h) }
func (h UnstructuredHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Less will return the older timestamp.  This way the oldest configmap is popped.
func (h UnstructuredHeap) Less(i, j int) bool {
	iTimestamp := h[i].GetCreationTimestamp()
	jTimestamp := h[j].GetCreationTimestamp()
	return iTimestamp.Before(&jTimestamp)
}

// Push and Pop use pointer receivers because they modify the slice's length,
// not just its contents.
func (h *UnstructuredHeap) Push(x interface{}) {
	*h = append(*h, x.(unstructured.Unstructured))
}

// Pop pops oldest configmap from the tree.
func (h *UnstructuredHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// The TestEnv config implements the configReader interface.
type TestEnv struct {
	Errors []error
	Vars   []EnvVar
}

func (env *TestEnv) getEnv(environment *RegionEnv) {
	env.Vars = append(env.Vars, EnvVar{Name: "Region", Value: environment.Region})
	return
}

func (env *TestEnv) parse(environment *RegionEnv) {
	environment.Vars = env.Vars
	return
}

// add missing elements of the second slice to the first.
func addReferences(a, b []v1.ObjectReference) []v1.ObjectReference {
	result := a
	for _, reference := range b {
		if !containsReference(result, reference) {
			result = append(result, reference)
		}
	}
	return result
}

// Determine if ObjectReference exists in list of Object References.
func containsReference(list []v1.ObjectReference, element v1.ObjectReference) bool {
	for _, reference := range list {
		if cmp.Equal(reference, element) {
			return true
		}
	}
	return false
}

// validateUnstructured ensures that all kubernetes resources contain the service name.
// This prevents different microservices from colliding.
// It also adds the name to the tags of each resource.
func validateUnstructured(object *unstructured.Unstructured, serviceName, deploymentName string) error {
	// Specific Object validation.
	if object.GetKind() == "HorizontalPodAutoscaler" {
		targetName, _, _ := unstructured.NestedString(object.Object, "spec", "scaleTargetRef", "name")
		if targetName != deploymentName {
			return fmt.Errorf("HPAutoscaler references %s, not the deployment %s", targetName, deploymentName)
		}
	}

	// Add App Label to K8s objects.
	appendLabel(object, "app", serviceName)

	// Verify name matches service name.
	name := object.GetName()

	// Ensure the logging sidecar's ConfigMap is associated with the service before name validation
	if name == "fluent-bit-sidecar-config" {
		name = fmt.Sprintf("%s-fluent-bit-config", serviceName)
	}
	if !strings.Contains(name, serviceName) {
		return fmt.Errorf("object name '%s' does not contain service name, %s", name, serviceName)
	}
	log.Debugf("validated object: %+v", object)
	return nil
}

// serviceFromObject converts a runtime object to an unstructured
func serviceFromObject(object runtime.Object, into *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	var err error
	if into == nil {
		into = &unstructured.Unstructured{}
	}
	into.Object, err = runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		return into, err
	}
	return into, err
}

func appendLabel(kubeObject metav1.Object, key string, val string) {
	if kubeObject.GetLabels() == nil {
		kubeObject.SetLabels(map[string]string{key: val})
	} else {
		labels := kubeObject.GetLabels()
		labels[key] = val
		kubeObject.SetLabels(labels)
	}
}

// Takes a domain string (e.g., "-dev.ethoscluster.ethos.adobe.net")
// and injects a subdomain at a given domain index in the string
// insertDomainPart("-dev.ethoscluster.ethos.adobe.net, 1, "corp")
// returns "-dev.corp.ethoscluster.ethos.adobe.net"
func insertDomainPart(domain string, at int, value string) string {
	domainSlice := strings.Split(domain, ".")
	// increase the slice capacity by 1
	domainSlice = append(domainSlice, "")
	// copy the contents of the slice at the given index one position to the right
	copy(domainSlice[at+1:], domainSlice[at:])
	// set the value at the desired index
	domainSlice[at] = value
	newDomain := strings.Join(domainSlice, ".")
	return newDomain
}

func lessObjectReference(x, y v1.ObjectReference) bool {
	return x.Name > y.Name
}

// removeInvalidCharacters strips characters which cannot be used in a secret key.
func removeInvalidCharacters(input string) string {
	regex := regexp.MustCompile(`[^a-zA-Z0-9.]*`)
	return regex.ReplaceAllString(input, "")
}
