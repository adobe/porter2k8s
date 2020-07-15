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
	"reflect"
	"strconv"
	"strings"
	"text/template"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	vaultapi "github.com/hashicorp/vault/api"
	catalogv1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	catalogscheme "github.com/kubernetes-sigs/service-catalog/pkg/client/clientset_generated/clientset/scheme"
	contourv1beta1 "github.com/projectcontour/contour/apis/contour/v1beta1"
	contourscheme "github.com/projectcontour/contour/apis/generated/clientset/versioned/scheme"
	log "github.com/sirupsen/logrus"
	istiov1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	istioscheme "istio.io/client-go/pkg/clientset/versioned/scheme"
	autoscaling "k8s.io/api/autoscaling/v2beta2"
	v1 "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	policy "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

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

func newVaultClient(address, token string) (*vaultapi.Client, error) {
	client, err := vaultapi.NewClient(&vaultapi.Config{
		Address: address,
	})
	if err != nil {
		return client, err
	}
	client.SetToken(token)
	return client, nil
}

func setIfZeroInt32(value, defaultValue int32) int32 {
	if value == 0 {
		return defaultValue
	}
	return value
}

// tokenReplace replaces a template with values from a dict, just like python's string.format.
func tokenReplaceString(format string, args map[string]string) string {
	var message bytes.Buffer

	tmpl, err := template.New("tokens").Parse(format)

	if err != nil {
		log.Errorf("Error creating template for replacing tokens: %s", err)
		return format
	}
	// Make the formatter return an error if the token is not found.
	tmpl.Option("missingkey=error")

	err = tmpl.Execute(&message, args)
	if err != nil {
		log.Warnf("Tokens unable to replace tokens, likely due to cloud type: %s", err)
		return format
	}
	return message.String()
}

// tokenReplace replaces a []byte template with values from a dict.
func tokenReplace(format []byte, args map[string]string) []byte {
	var message bytes.Buffer

	tmpl, err := template.New("tokens").Parse(string(format))

	if err != nil {
		log.Errorf("Error creating template for replacing tokens: %s", err)
		return format
	}
	// Make the formatter return an error if the token is not found.
	tmpl.Option("missingkey=error")

	err = tmpl.Execute(&message, args)
	if err != nil {
		log.Warnf("Tokens unable to replace tokens, likely due to cloud type: %s", err)
		return format
	}
	return message.Bytes()
}

// kubeDecoder reads Kubernetes deployment, service, etc yaml files.
func kubeDecoder(fileName string) (interface{}, error) {
	fileContent, readErr := ioutil.ReadFile(fileName)
	if readErr != nil {
		return nil, readErr
	}
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, _, err := decode([]byte(fileContent), nil, nil)
	return obj, err
}

// multiDecoder reads multiple Kubernetes objects from the same file.
func multiDecoder(filename string) (objects []Decoded, err error) {
	data, readErr := ioutil.ReadFile(filename)
	if readErr != nil {
		return objects, readErr
	}
	// Create scheme with istio objects.
	istioscheme.AddToScheme(scheme.Scheme)
	catalogscheme.AddToScheme(scheme.Scheme)
	contourscheme.AddToScheme(scheme.Scheme)

	r := ioutil.NopCloser(bytes.NewReader(data))
	decoder := yaml.NewDocumentDecoder(r)
	var chunk []byte
	for {
		chunk = make([]byte, len(data))
		_, err = decoder.Read(chunk)
		if err != nil {
			break
		}
		chunk = bytes.Trim(chunk, "\x00")

		decode := scheme.Codecs.UniversalDeserializer().Decode
		obj, gvk, err := decode([]byte(chunk), nil, nil)
		log.Infof("gvk: %+v", gvk)
		if err != nil {
			log.Errorf("Error: %v", err)
			break
		}
		objects = append(objects, Decoded{Object: obj, GVK: gvk.String()})
	}
	// No need to pass EOF error to caller.
	if err == io.EOF {
		err = nil
	}
	return objects, err
}

// ConfigMapHeap is a heap in which each node is the oldest configmap in its subtree.
type ConfigMapHeap []v1.ConfigMap

func (h ConfigMapHeap) Len() int      { return len(h) }
func (h ConfigMapHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Less will return the older timestamp.  This way the oldest configmap is popped.
func (h ConfigMapHeap) Less(i, j int) bool {
	return h[i].ObjectMeta.CreationTimestamp.Before(&h[j].ObjectMeta.CreationTimestamp)
}

// Push and Pop use pointer receivers because they modify the slice's length,
// not just its contents.
func (h *ConfigMapHeap) Push(x interface{}) {
	*h = append(*h, x.(v1.ConfigMap))
}

// Pop pops oldest configmap from the tree.
func (h *ConfigMapHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// SecretHeap is a heap in which each node is the oldest secret in its subtree.
type SecretHeap []v1.Secret

func (h SecretHeap) Len() int      { return len(h) }
func (h SecretHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Less will return the older timestamp.  This way the oldest secret is popped.
func (h SecretHeap) Less(i, j int) bool {
	return h[i].ObjectMeta.CreationTimestamp.Before(&h[j].ObjectMeta.CreationTimestamp)
}

// Push and Pop use pointer receivers because they modify the slice's length,
// not just its contents.
func (h *SecretHeap) Push(x interface{}) {
	*h = append(*h, x.(v1.Secret))
}

// Pop pops oldest secret from the tree.
func (h *SecretHeap) Pop() interface{} {
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

// needsUpdate compares two kubernetes objects. It returns true if they differ.
func needsUpdate(current, update interface{}) bool {
	equal := false
	ignoredTypes := cmpopts.IgnoreTypes(metav1.TypeMeta{})
	ignoredMetaData := cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "SelfLink", "UID",
		"CreationTimestamp", "Namespace", "Generation")
	switch asserted := current.(type) {
	case *v1.Service:
		currentAsserted := asserted
		updateAsserted := update.(*v1.Service)
		// Cluster IP is set by the cluster unless otherwise specified. It cannot be changed with an update.
		equal = cmp.Equal(currentAsserted.Spec, updateAsserted.Spec, cmpopts.IgnoreFields(v1.ServiceSpec{},
			"ClusterIP"))
	case *v1.ServiceAccount:
		currentAsserted := asserted
		updateAsserted := update.(*v1.ServiceAccount)
		equal = cmp.Equal(currentAsserted, updateAsserted, ignoredMetaData, ignoredTypes,
			cmpopts.SortSlices(lessObjectReference))
	case *extensionsv1beta1.Ingress:
		currentAsserted := asserted
		updateAsserted := update.(*extensionsv1beta1.Ingress)
		equal = cmp.Equal(currentAsserted.Spec, updateAsserted.Spec)
	case *contourv1beta1.IngressRoute:
		currentAsserted := asserted
		updateAsserted := update.(contourv1beta1.IngressRoute)
		equal = cmp.Equal(currentAsserted.Spec, updateAsserted.Spec)
	case *autoscaling.HorizontalPodAutoscaler:
		currentAsserted := asserted
		updateAsserted := update.(*autoscaling.HorizontalPodAutoscaler)
		// The comparison will panic for object type target value unless AllowUnexported is set.
		// TargetValue is of type resource.Quantity which contains unexported fields of unexported types.
		// The target value for an HPA metric is obviously not something that can be ignored.
		allowed := deepAllowUnexported(resource.Quantity{})
		equal = cmp.Equal(currentAsserted.Spec, updateAsserted.Spec, allowed)
	case *istiov1beta1.Gateway:
		currentAsserted := asserted
		updateAsserted := update.(*istiov1beta1.Gateway)
		equal = cmp.Equal(currentAsserted.Spec, updateAsserted.Spec)
	case *istiov1beta1.VirtualService:
		currentAsserted := asserted
		updateAsserted := update.(*istiov1beta1.VirtualService)
		equal = cmp.Equal(currentAsserted.Spec, updateAsserted.Spec)
	case *v1.ConfigMap:
		currentAsserted := asserted
		updateAsserted := update.(*v1.ConfigMap)
		equal = cmp.Equal(currentAsserted.Data, updateAsserted.Data)
	case *policy.PodDisruptionBudget:
		currentAsserted := asserted
		updateAsserted := update.(*policy.PodDisruptionBudget)
		equal = cmp.Equal(currentAsserted.Spec, updateAsserted.Spec)
	case *catalogv1beta1.ServiceInstance:
		currentAsserted := asserted
		updateAsserted := update.(*catalogv1beta1.ServiceInstance)
		equal = cmp.Equal(currentAsserted.Spec.Parameters, updateAsserted.Spec.Parameters)
	case *catalogv1beta1.ServiceBinding:
		currentAsserted := asserted
		updateAsserted := update.(*catalogv1beta1.ServiceBinding)
		// Ignore ExternalID and UserInfo, since those are cluster assigned.
		equal = cmp.Equal(currentAsserted.Spec, updateAsserted.Spec,
			cmpopts.IgnoreFields(catalogv1beta1.ServiceBindingSpec{}, "ExternalID", "UserInfo"))
	case *v1.Secret:
		currentAsserted := asserted
		updateAsserted := update.(v1.Secret)
		equal = cmp.Equal(currentAsserted.StringData, updateAsserted.StringData)
	default:
		log.Infof("Unsupported Type %T", asserted)
	}
	if !equal {
		log.Info("Update required.")
	} else {
		log.Info("No update required.")
	}
	return !equal
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

// deepAllowUnexported compares nested unexported types.
// This is required for the HPA.
func deepAllowUnexported(vs ...interface{}) cmp.Option {
	m := make(map[reflect.Type]struct{})
	for _, v := range vs {
		structTypes(reflect.ValueOf(v), m)
	}
	var typs []interface{}
	for t := range m {
		typs = append(typs, reflect.New(t).Elem().Interface())
	}
	return cmp.AllowUnexported(typs...)
}

// structTypes recurses through the possible types.
func structTypes(v reflect.Value, m map[reflect.Type]struct{}) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			structTypes(v.Elem(), m)
		}
	case reflect.Interface:
		if !v.IsNil() {
			structTypes(v.Elem(), m)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			structTypes(v.Index(i), m)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			structTypes(v.MapIndex(k), m)
		}
	case reflect.Struct:
		m[v.Type()] = struct{}{}
		for i := 0; i < v.NumField(); i++ {
			structTypes(v.Field(i), m)
		}
	}
}

// validateObject ensures that all kubernetes resources contain the service name.
// This prevents different microservices from colliding.
// It also adds the name to the tags of each resource.
func validateObject(object interface{}, serviceName, deploymentName string) error {

	// Process things that may contain K8s objects and filter out nil value interfaces.
	v := reflect.ValueOf(object)
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			validateObject(v.Index(i).Interface(), serviceName, deploymentName)
		}
		return nil
	case reflect.Map:
		for _, k := range v.MapKeys() {
			validateObject(v.MapIndex(k).Interface(), serviceName, deploymentName)
		}
		return nil
	// Only pointers to kubernetes objects will assert as runtime.Object below.  However, to account for Kubernetes
	// List objects // (e.g., DeploymentList, IngressRouteList) that contain a slice field of structs, we will check to
	// see if the element of the pointer has an "Items" array, then pass the objects through this function.
	case reflect.Ptr:
		// Some fields may never have been assigned, so filter out Interfaces with a nil value, before they break the
		// next type assertion.
		if v.IsNil() {
			return nil
		}
		// Check to see if this is a Kubernetes List object, and if so, validate its items
		element := v.Elem()
		if element.Kind() == reflect.Struct {
			listItems := element.FieldByName("Items")
			if listItems.IsValid() && listItems.Kind() == reflect.Slice {
				for i := 0; i < listItems.Len(); i++ {
					validateObject(listItems.Index(i), serviceName, deploymentName)
				}
				return nil
			}
		}
	}

	// Specific Object validation.
	if asserted, ok := object.(*autoscaling.HorizontalPodAutoscaler); ok {
		if asserted.Spec.ScaleTargetRef.Name != deploymentName {
			return fmt.Errorf("HPAutoscaler references %s, not the deployment %s",
				asserted.ObjectMeta.Name,
				deploymentName)
		}
	}

	switch asserted := object.(type) {
	// Common Object validation.
	case metav1.Object:
		// Add App Label to K8s objects.
		appendLabel(asserted, "app", serviceName)

		// Verify name matches service name.
		name := asserted.GetName()

		// Ensure the logging sidecar's ConfigMap is associated with the service before name validation
		if name == "fluent-bit-sidecar-config" {
			name = fmt.Sprintf("%s-fluent-bit-config", serviceName)
		}
		if !strings.Contains(name, serviceName) {
			return fmt.Errorf("object name '%s' does not contain service name, %s", name, serviceName)
		}
		log.Debugf("validated object: %+v", object)
		return nil

	// Not a K8s object.
	case ClusterConfigs:
		return nil
	default:
		return fmt.Errorf("Unknown object type %+v", asserted)
	}
}

func appendLabel(kubeObject metav1.Object, key string, val string) {
	if kubeObject.GetLabels() == nil {
		kubeObject.SetLabels(map[string]string{key: val})
	} else {
		kubeObject.GetLabels()[key] = val
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
