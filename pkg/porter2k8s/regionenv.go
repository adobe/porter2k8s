package porter2k8s

import (
	"container/heap"
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/itchyny/gojq"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimeyaml "k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// RegionEnv is the cluster/region specific configuration.
type RegionEnv struct {
	Cfg             *CmdConfig
	Clientset       kubernetes.Interface // kubernetes.Clientset or testclient.Clientset
	ClusterSettings map[string]string    // Settings for porter2k8s.
	Context         context.Context
	DynamicClient   dynamic.Interface
	Errors          []error
	Logger          *log.Entry
	Mapper          meta.RESTMapper
	ObjectRefs      map[string][]string // name_kind: [path1, path2,...]
	PodObject       *unstructured.Unstructured
	Region          string
	SecretKeyRefs   []SecretKeyRef
	Secrets         []SecretRef
	Unstructured    []*unstructured.Unstructured
	Vars            []EnvVar
}

// RegionEnv Methods

// deleteJob deletes the specified job on K8s cluster.
// if wait is true, blocks until job deletion is completed
// if wait is false, returns once the cluster has received the delete request; the operation may still be in progress
func (regionEnv *RegionEnv) deleteJob(job *unstructured.Unstructured, wait bool) error {
	gvk := schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	regionEnv.Logger.Infof("Deleting Job %s", job.GetName())
	mapping, _ := regionEnv.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace(regionEnv.Cfg.Namespace)

	propagation := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: int64Ref(0),
		PropagationPolicy:  &propagation,
	}

	if wait {
		listOptions := metav1.ListOptions{
			FieldSelector:  fmt.Sprintf("metadata.name=%s", job.GetName()),
			TimeoutSeconds: int64Ref(30),
		}
		watcher, err := dynamicInterface.Watch(regionEnv.Context, listOptions)
		if err != nil {
			return err
		}
		defer watcher.Stop()

		watchCh := watcher.ResultChan()
		err = dynamicInterface.Delete(regionEnv.Context, job.GetName(), deleteOptions)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}
		for {
			select {
			case <-regionEnv.Context.Done():
				return regionEnv.Context.Err()

			case event, ok := <-watchCh:
				if !ok {
					return fmt.Errorf("timeout deleting job")
				}

				if event.Type == watch.Deleted {
					return nil
				}
				if event.Type == watch.Error {
					regionEnv.Logger.Warnf("error reported during job deletion: %v", event.Object)
				}
			}
		}
	} else {
		err := dynamicInterface.Delete(regionEnv.Context, job.GetName(), deleteOptions)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

// deleteOldEnv keeps only the newest (maxConfigMaps) number of secrets or configmaps for the given application,
// with the exception of that being used by the currently running container, which was created by the previous
// successful deployment.
func (regionEnv *RegionEnv) deleteOldEnv(deploymentName string, protectedSha map[string]bool, objType string) bool {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: objType}
	mapping, _ := regionEnv.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace(regionEnv.Cfg.Namespace)
	listOptions := metav1.ListOptions{
		LabelSelector:  fmt.Sprintf("app=%s,retain!=true", deploymentName),
		TimeoutSeconds: int64Ref(30),
	}
	deleteOptions := metav1.DeleteOptions{}
	current, listErr := dynamicInterface.List(regionEnv.Context, listOptions)
	if listErr != nil {
		regionEnv.errf("Error listing %s:\n%v", objType, listErr)
		return false
	}

	// Filter out items without sha label.
	items := []unstructured.Unstructured{}
	for _, item := range current.Items {
		if _, ok := item.GetLabels()["sha"]; ok {
			items = append(items, item)
		}
	}
	if len(items) <= regionEnv.Cfg.MaxConfigMaps {
		return true
	}
	// Delete old CM/Secrets until we have fewer than what is desired.
	regionEnv.Logger.Infof("Number of %s for application exceed limit of %d", objType, regionEnv.Cfg.MaxConfigMaps)
	var currentHeap UnstructuredHeap = items
	heap.Init(&currentHeap)
	for len(currentHeap) > regionEnv.Cfg.MaxConfigMaps {
		old := heap.Pop(&currentHeap).(unstructured.Unstructured)
		if _, ok := protectedSha[old.GetLabels()["sha"]]; ok {
			regionEnv.Logger.Infof("Not deleting %s %s as it is a protected SHA", objType, old.GetName())
		} else {
			regionEnv.Logger.Infof("Deleting old %s %s", objType, old.GetName())
			deleteErr := dynamicInterface.Delete(regionEnv.Context, old.GetName(), deleteOptions)
			if deleteErr != nil {
				regionEnv.errf("Error deleting %s:\n%v", objType, deleteErr)
				return false
			}
		}
	}
	return true
}

// Sprig functions are only available with this method. This will be the default
// as we move to the unstructured client for all objects.
// YAML must be used because go template can't handle escaped characters.
// for example {{ splitList ",".support_subnets | toJson }}
func (regionEnv *RegionEnv) replaceDynamicParameters(obj *unstructured.Unstructured) {
	gvk := obj.GroupVersionKind()
	data, _ := yaml.Marshal(obj.Object)
	dataReplace := tokenReplace(data, regionEnv.ClusterSettings, regionEnv.Logger)
	unstructuredDecoder := runtimeyaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	replacementRuntimeObject, _, err := unstructuredDecoder.Decode(dataReplace, &gvk, nil)
	if err != nil {
		regionEnv.errf("Unable to Unmarshal Yaml. Original: %s\nReplacement:%s\nError:%s\n",
			data, dataReplace, err)
		return
	}
	_, err = serviceFromObject(replacementRuntimeObject, obj)
	if err != nil {
		regionEnv.errf("Runtime object to unstructured failed. Original: %s\nReplacement:%s\nError:%s",
			obj.Object, replacementRuntimeObject, err)
		return
	}

	regionEnv.Logger.Debugf("Token Replace Result %s", *obj)
}

// updateGateway replaces the gateway of the Istio Virtual service if the "GATEWAY" key exists within
// the ClusterSettings.
func (regionEnv *RegionEnv) updateGateway() {
	gateways, ok := regionEnv.ClusterSettings["GATEWAYS"]
	if !ok {
		regionEnv.Logger.Info(
			"No local Istio gateway found.",
		)
		return
	}
	gatewaySlice := strings.Fields(gateways)
	for _, vs := range regionEnv.findUnstructured("networking.istio.io/v1beta1", "VirtualService") {
		unstructured.SetNestedStringSlice(vs.Object, gatewaySlice, "spec", "gateways")
		regionEnv.Logger.Infof("Changed gateways to %s", gatewaySlice)
	}
}

// updateRegistry replaces the registry of docker image within deployment if the "REGISTRY" key exists within
// the ClusterSettings.
func (regionEnv *RegionEnv) updateRegistry() {
	registry, ok := regionEnv.ClusterSettings["REGISTRY"]
	if !ok {
		regionEnv.Logger.Infof("No local registry found for %s.", regionEnv.Region)
		return
	}
	// Replace registry in the docker image, by substituting text up to the first slash.
	// Ex. 983073263818.dkr.ecr.us-west-2.amazonaws.com/echosign/background-removal ->
	// 983073263818.dkr.ecr.us-east-1.amazonaws.com/echosign/background-removal
	registry = fmt.Sprintf("%s/$1", registry)
	regex := regexp.MustCompile(`.*?/(.*)`)
	applicationContainer, typedObject := findContainer(regionEnv.PodObject)
	if applicationContainer == nil {
		regionEnv.Logger.Errorf("Application container not found! Region: %s", regionEnv.Region)
		return
	}
	applicationContainer.Image = regex.ReplaceAllString(applicationContainer.Image, registry)
	regionEnv.Logger.Infof("Changed image to %s", applicationContainer.Image)

	regionEnv.placePodObject(typedObject)
}

// updateDomainName replaces the domain name of the object with the cluster value if the "DOMAIN" key exists within
// the ClusterSettings. This assumes that all domain names need to be changed to a different value.
// This may need to be smarter in the future.
func (regionEnv *RegionEnv) updateDomainName() {
	domainList, ok := regionEnv.ClusterSettings["DOMAIN"]
	if !ok {
		regionEnv.Logger.Infof("No local domain name found for %s.", regionEnv.Region)
		return
	}

	// Replace domain in the domain name, by substituting text up to the first period.
	// Cluster may have multiple domain names.  Therefore multiple new domains may replace the single original domain.
	// Ex. foo.bar -> foo.domain1, foo.domain2

	// Change cluster domain list into strings for regex.
	domains := strings.Split(domainList, ",")
	for i, domain := range domains {
		domains[i] = fmt.Sprintf("$1%s", domain)
	}
	regex := regexp.MustCompile(`(.*?)\..*`)
	regionEnv.Logger.Infof("Domains: %s", domains)

	// IngressRoutes have at most one virtualhost, so if multiple domains are given,
	// a new IngressRoute has to be created for each one.  The domain replacements
	// are determined based on the contour annotation "kubernetes.io/ingress.class".
	// For ethos-k8s, the DOMAIN setting is that of the ethos cluster:  <cluster-name>.ethos.adobe.net
	// Ingress classes of contour-internal and contour-corp have an additional domain component
	// of "int" or "corp" inserted between the service name and the cluster domain.
	// https://git.corp.adobe.com/adobe-platform/k8s-infrastructure/blob/master/docs/user-guide.md#ingress-dns-names

	ingressRoutes := regionEnv.findUnstructured("contour.heptio.com/v1beta1", "IngressRoute")
	if len(domains) == 1 && domains[0] != "" && len(ingressRoutes) > 0 {
		clusterDomain := domains[0]
		for _, ingressRoute := range ingressRoutes {
			var newFqdn string
			fqdn, _, _ := unstructured.NestedString(ingressRoute.Object, "spec", "virtualhost", "fqdn")
			ingressClass, _, ingressAnnotationErr := unstructured.NestedString(
				ingressRoute.Object, "metadata", "annotations", "kubernetes.io/ingress.class",
			)
			tlsSecret, tlsSecretFound, _ := unstructured.NestedString(
				ingressRoute.Object, "spec", "virtualhost", "tls", "secretName",
			)
			if !tlsSecretFound {
				regionEnv.Logger.Warnf("No TLS secret listed for ingress route %s", fqdn)
			}
			switch ingressClass {
			case "contour-corp":
				// prefix base cluster domain with "corp", per ethos cluster domain convention:
				newCorpDomain := insertDomainPart(clusterDomain, 1, "corp")
				newFqdn = regex.ReplaceAllString(fqdn, newCorpDomain)
				unstructured.SetNestedField(ingressRoute.Object, newFqdn, "spec", "virtualhost", "fqdn")
				// Sanity check the SecretName if TLS is in use
				if tlsSecretFound && tlsSecret != "heptio-contour/cluster-ssl-corp" {
					regionEnv.Logger.Errorf(
						"Invalid IngressRoute class + TLS SecretName combination: contour-corp + %s", tlsSecret,
					)
				}
			case "contour-internal":
				// prefix base cluster domain with "int"
				newInternalDomain := insertDomainPart(clusterDomain, 1, "int")
				newFqdn = regex.ReplaceAllString(fqdn, newInternalDomain)
				unstructured.SetNestedField(ingressRoute.Object, newFqdn, "spec", "virtualhost", "fqdn")
				// Sanity check the SecretName if TLS is in use
				if tlsSecretFound && tlsSecret != "heptio-contour/cluster-ssl-int" {
					regionEnv.Logger.Errorf(
						"Invalid IngressRoute class + TLS SecretName combination: contour-internal + %s", tlsSecret,
					)
				}
			case "contour-public":
				// replace the fqdn only if the cluster TLS cert is in use
				if tlsSecretFound && tlsSecret == "heptio-contour/cluster-ssl-public" {
					newFqdn = regex.ReplaceAllString(fqdn, clusterDomain)
					unstructured.SetNestedField(ingressRoute.Object, newFqdn, "spec", "virtualhost", "fqdn")
				}
			default:
				regionEnv.Logger.Errorf(
					"Invalid kubernetes.io/ingress.class:  %s, %s", ingressClass, ingressAnnotationErr,
				)
			}
			regionEnv.Logger.Infof("Added IngressRoute for fqdn: %s -> %s", fqdn, newFqdn)
		}
	} else if len(domains) > 1 && len(ingressRoutes) > 0 {
		regionEnv.Logger.Warnf("Ingress Routes cannot accomadate multiple domains %s", domains)
	}

	for _, gw := range regionEnv.findUnstructured("networking.istio.io/v1beta1", "Gateway") {
		var newServers []interface{}
		servers, serversFound, _ := unstructured.NestedSlice(gw.Object, "spec", "servers")
		for _, server := range servers {
			var newHosts []string
			serverMap, ok := server.(map[string]interface{})
			if !ok {
				regionEnv.errf("Malformed Istio Gateway missing server entries %+v", gw)
			}
			hosts, hostsFound, _ := unstructured.NestedStringSlice(serverMap, "hosts")
			for _, host := range hosts {
				for _, domain := range domains {
					newHosts = append(newHosts, regex.ReplaceAllString(host, domain))
				}
			}
			if hostsFound {
				regionEnv.Logger.Infof("Changed Gateway domain to %s", newHosts)
				unstructured.SetNestedStringSlice(serverMap, newHosts, "hosts")
			}
			newServers = append(newServers, server)
		}
		if serversFound {
			unstructured.SetNestedSlice(gw.Object, newServers, "spec", "servers")
		}
	}

	virtualServices := regionEnv.findUnstructured("networking.istio.io/v1beta1", "VirtualService")
	if len(virtualServices) > 0 {
		regionEnv.updateGateway()
	}
	for _, vs := range virtualServices {
		var newHosts []string
		hosts, hostsFound, _ := unstructured.NestedStringSlice(vs.Object, "spec", "hosts")
		for _, host := range hosts {
			for _, domain := range domains {
				newHosts = append(newHosts, regex.ReplaceAllString(host, domain))
			}
		}
		if hostsFound {
			unstructured.SetNestedStringSlice(vs.Object, newHosts, "spec", "hosts")
			regionEnv.Logger.Infof("Changed Virtual Service domain to %s", newHosts)
		}
	}
}

// createConfigMap creates the deployment configmap.
func (regionEnv *RegionEnv) createConfigMap(deploymentName string) bool {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
	mapping, _ := regionEnv.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace(regionEnv.Cfg.Namespace)
	data := make(map[string]interface{})
	for _, envVar := range regionEnv.Vars {
		data[envVar.Name] = envVar.Value
	}
	// Use Kubernetes labels to easily find and delete old configmaps.
	configMap := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": fmt.Sprintf("%s-%s", deploymentName, regionEnv.Cfg.SHA),
				"labels": map[string]interface{}{
					"app": deploymentName,
					"sha": regionEnv.Cfg.SHA,
				},
			},
			"data": data,
		},
	}

	_, configMapGetErr := dynamicInterface.Get(regionEnv.Context, configMap.GetName(), metav1.GetOptions{})

	// ConfigMap may already exist if this is a rollback.
	if configMapGetErr == nil {
		regionEnv.Logger.Info("Config Map already exists")
		return true
	}
	if !errors.IsNotFound(configMapGetErr) {
		regionEnv.errf("Unexpected ConfigMap get error\n%s", configMapGetErr)
		return false
	}

	configMapUploaded, configMapErr := dynamicInterface.Create(regionEnv.Context, &configMap,
		metav1.CreateOptions{FieldManager: "porter2k8s"})
	if configMapErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, configMapErr)
		return false
	}
	regionEnv.Logger.Infof("Created Config Map\n%+v", configMapUploaded)

	return true
}

// createSecret creates the deployment secret.
func (regionEnv *RegionEnv) createSecret(deploymentName string) bool {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	mapping, _ := regionEnv.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace(regionEnv.Cfg.Namespace)

	secretData := make(map[string]interface{})
	for _, secretRef := range regionEnv.Secrets {
		if secretRef.Kind == "container" {
			secretData[secretRef.Name] = base64.StdEncoding.EncodeToString([]byte(secretRef.Value))
		}
	}

	// Use Kubernetes labels to easily find and delete old secrets.
	secret := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name": fmt.Sprintf("%s-%s", deploymentName, regionEnv.Cfg.SHA),
				"labels": map[string]interface{}{
					"app": deploymentName,
					"sha": regionEnv.Cfg.SHA,
				},
			},
			"data": secretData,
		}}

	_, secretGetErr := dynamicInterface.Get(regionEnv.Context, secret.GetName(), metav1.GetOptions{})

	// Secret may already exist if this is a rollback.
	if secretGetErr == nil {
		regionEnv.Logger.Info("Secret already exists")
		return true
	}
	if !errors.IsNotFound(secretGetErr) {
		regionEnv.errf("Unexpected Secret get error\n%s", secretGetErr)
		return false
	}

	_, secretErr := dynamicInterface.Create(regionEnv.Context, &secret,
		metav1.CreateOptions{FieldManager: "porter2k8s"})
	if secretErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, secretErr)
		return false
	}
	regionEnv.Logger.Infof("Created Secret %s", secret.GetName())

	return true
}

// updateHPAMin replaces the minimum Horizontal Pod Autoscaler value with cluster specific values.
func (regionEnv *RegionEnv) updateHPAMinimum() {
	clusterMinStr, ok := regionEnv.ClusterSettings["HPAMIN"]
	if !ok {
		regionEnv.Logger.Info("No local horizontal pod autoscaler minimum value found.")
		return
	}
	clusterMin, _ := strconv.ParseInt(clusterMinStr, 10, 64)
	// Maximum setting for an HPA Minimum.
	clusterMinMaxStr, clusterMinMaxExists := regionEnv.ClusterSettings["HPAMINMAX"]
	if !clusterMinMaxExists {
		regionEnv.Logger.Info("No local horizontal pod autoscaler maximum value found for the HPA minimum.")
	}
	clusterMinMax, _ := strconv.ParseInt(clusterMinMaxStr, 10, 64)
	for _, autoscaler := range regionEnv.findUnstructured("autoscaling/v2beta2", "HorizontalPodAutoscaler") {
		currentMin, _, _ := unstructured.NestedInt64(autoscaler.Object, "spec", "minReplicas")
		currentMax, _, _ := unstructured.NestedInt64(autoscaler.Object, "spec", "maxReplicas")
		if currentMin < clusterMin {
			unstructured.SetNestedField(autoscaler.Object, clusterMin, "spec", "minReplicas")
			regionEnv.Logger.Infof("Raising HPA minimum to cluster minimum of %d.", clusterMin)
		}
		if currentMax < clusterMin {
			unstructured.SetNestedField(autoscaler.Object, clusterMin, "spec", "maxReplicas")
		}
		if currentMin > clusterMinMax && clusterMinMaxExists {
			unstructured.SetNestedField(autoscaler.Object, clusterMinMax, "spec", "minReplicas")
			regionEnv.Logger.Infof("Lowering HPA minimum to cluster maximum of %d.", clusterMinMax)
		}
	}
}

// identifySecrets creates an environment variables named ENV_VARS_TO_REDACT, which contains the names
// of the secret environment variables names.
func (regionEnv *RegionEnv) identifySecrets() {
	var sb strings.Builder
	numSecrets := len(regionEnv.Secrets)
	for i, secret := range regionEnv.Secrets {
		sb.WriteString(secret.Name)
		if i < numSecrets-1 {
			sb.WriteString(",")
		}
	}
	var redacted EnvVar
	redacted.Name = "ENV_VARS_TO_REDACT"
	redacted.Value = sb.String()
	regionEnv.Vars = append(regionEnv.Vars, redacted)
}

// findUnstructured returns all unstructured objects of matching an API Version and Kind, or just the APIVersion if
// the Kind is empty.
func (regionEnv *RegionEnv) findUnstructured(apiVersion, kind string) []*unstructured.Unstructured {
	var output []*unstructured.Unstructured
	for _, service := range regionEnv.Unstructured {
		if kind == "" {
			if service.GetAPIVersion() == apiVersion {
				regionEnv.Logger.Infof("Found %s", apiVersion)
				output = append(output, service)
			}
		} else {
			if service.GetAPIVersion() == apiVersion && service.GetKind() == kind {
				regionEnv.Logger.Infof("Found %s %s", apiVersion, kind)
				output = append(output, service)
			}
		}
	}
	if len(output) == 0 {
		regionEnv.Logger.Infof("Found no %s %s", apiVersion, kind)
	}

	return output
}

// Inject secret key references into container.
func (regionEnv *RegionEnv) injectSecretKeyRefs() {
	applicationContainer, typedObject := findContainer(regionEnv.PodObject)
	if applicationContainer == nil {
		regionEnv.errf("Unable to find application image in deployment spec")
	}
	for _, secretKeyRef := range regionEnv.SecretKeyRefs {
		envVar := v1.EnvVar{
			Name: secretKeyRef.Name,
			ValueFrom: &v1.EnvVarSource{
				SecretKeyRef: &v1.SecretKeySelector{
					LocalObjectReference: v1.LocalObjectReference{Name: secretKeyRef.Secret},
					Key:                  secretKeyRef.Key,
					// Secret must exist.
					Optional: boolRef(false),
				},
			},
		}
		applicationContainer.Env = append(applicationContainer.Env, envVar)
	}
	// Back to unstructured
	newUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(typedObject)
	if err != nil {
		regionEnv.errf(err.Error())
	}
	regionEnv.PodObject.Object = newUnstructured
}

// Delete old configmaps.
func (regionEnv *RegionEnv) postDeployCleanup() error {
	deploymentName := regionEnv.PodObject.GetName()
	// Do not delete configmaps or secret objects associated with the current or previously deployed sha.
	protectedShas := map[string]bool{
		regionEnv.PodObject.GetLabels()["sha"]: true,
		regionEnv.Cfg.SHA:                      true,
	}
	// Delete old configmaps.
	configMapDeleteSucceeded := regionEnv.deleteOldEnv(deploymentName, protectedShas, "ConfigMap")
	if !configMapDeleteSucceeded {
		return fmt.Errorf("exiting on configmap deletion error")
	}
	// Delete old secrets.
	secretDeleteSucceeded := regionEnv.deleteOldEnv(deploymentName, protectedShas, "Secret")
	if !secretDeleteSucceeded {
		return fmt.Errorf("exiting on secret deletion failure")
	}
	return nil
}

// porter2k8sConfigMap fetches cluster specific porter2k8s configmap, if it exists.
func (regionEnv *RegionEnv) porter2k8sConfigMap() {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
	mapping, _ := regionEnv.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace(regionEnv.Cfg.Namespace)
	configMap, configMapErr := dynamicInterface.Get(regionEnv.Context, "porter2k8s", metav1.GetOptions{})
	// Fall back to the porter2k8s configmap if it exists
	if errors.IsNotFound(configMapErr) {
		regionEnv.Logger.Info("No cluster specific porter2k8s config found.  Looking for porter2k8s config instead.")
		configMap, configMapErr = dynamicInterface.Get(regionEnv.Context, "porter2k8s", metav1.GetOptions{})
		if errors.IsNotFound(configMapErr) {
			regionEnv.Logger.Info("No cluster specific porter2k8s config found.")
		}
	}
	if configMapErr == nil {
		data, _, _ := unstructured.NestedStringMap(configMap.Object, "data")
		// Overwrite cluster settings from the namespace with those from the service's configuration.
		for key, value := range regionEnv.ClusterSettings {
			data[key] = value
		}
		regionEnv.ClusterSettings = data
		return
	}
	regionEnv.Logger.Errorf("unexpected Error retrieving porter2k8s configMap.\n%v", configMapErr)
	return
}

// Validate to make sure that kubernetes services use the correct Secret path
// so that it will prevent microservices from colliding
// KubeObject validation is done in helper.go (validatekubeobject)
func (regionEnv *RegionEnv) validateSecretPath(deploymentName string) error {

	if regionEnv.Cfg.SecretPathWhiteList == "" {
		// Secret Path validation is not performed if no whitelist terms are provided.
		return nil
	}

	// Add the service's name to the whitelist.
	trimmedDeploymentName := strings.ReplaceAll(deploymentName, "-", "")
	whitelist := append(strings.Split(regionEnv.Cfg.SecretPathWhiteList, " "), trimmedDeploymentName)

	for _, secret := range regionEnv.Secrets {
		validPath := false
		for _, whitelistTerm := range whitelist {
			if strings.Contains(strings.ToLower(secret.Path), strings.ToLower(whitelistTerm)) {
				validPath = true
				break
			}
		}
		if validPath == false {
			return fmt.Errorf(
				"Service is not allowed access to secret path %s, must contain one of %s", secret.Path, whitelist,
			)
		}
	}
	return nil
}

// sortUnstructured sorts removes N/A objects and puts pods at the end.
func (regionEnv *RegionEnv) sortUnstructured() {
	var newList []*unstructured.Unstructured

	for _, obj := range regionEnv.Unstructured {
		apiVersion := obj.GetAPIVersion()
		// Don't add to the list if it is the wrong networking object or if it has pods.
		if (apiVersion == "networking.istio.io/v1beta1" && regionEnv.ClusterSettings["ISTIO"] != "true") ||
			(apiVersion == "contour.heptio.com/v1beta1" && regionEnv.ClusterSettings["ISTIO"] == "true") {
			continue
		}
		newList = append(newList, obj)
	}
	regionEnv.Unstructured = newList
}

func (regionEnv *RegionEnv) placePodObject(typedObject interface{}) {
	// Back to unstructured
	newUnstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(typedObject)
	if err != nil {
		regionEnv.errf("Unable to convert %s back to unstructured. %s", typedObject, err)
	}
	regionEnv.PodObject.Object = newUnstructured
}

// err append region to error.
func (regionEnv *RegionEnv) errf(format string, a ...interface{}) {
	regionFormat := fmt.Sprintf("Region: %s, %s", regionEnv.Region, format)
	regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf(regionFormat, a...))
}

// Inject a sidecar if one is provided in the cluster settings.
func (regionEnv *RegionEnv) addSidecarContainer() {
	// Find the fluent-bit container in the sidecar deployment file and append it to the list of containers in the
	// service deployment object
	sidecarRaw, ok := regionEnv.ClusterSettings["SIDECAR"]
	if !ok {
		regionEnv.Logger.Infof("No sidecar injection")
		return
	}
	regionEnv.Logger.Infof("Attempting to inject sidecar container and volumes to %s", regionEnv.PodObject.GetName())
	// Marshal to unstructured.
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	unstructuredDecoder := runtimeyaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	sidecarObj, _, err := unstructuredDecoder.Decode([]byte(sidecarRaw), &gvk, nil)
	if err != nil || sidecarObj == nil {
		regionEnv.errf("Unable to Unmarshal sidecar %s\nError:%s\n",
			sidecarRaw, err)
		return
	}
	sidecar, err := serviceFromObject(sidecarObj, nil)
	if err != nil {
		regionEnv.errf("Failed to convert Sidecar runtime object to unstructured failed. Runtime Object: %s\nError:%s",
			sidecarObj, err)
		return
	}

	// Append container to pod object (Deployment, Statefulset, Job, etc).
	deploymentContainers, _, _ := unstructured.NestedSlice(
		regionEnv.PodObject.Object, "spec", "template", "spec", "containers",
	)
	sidecarContainers, _, _ := unstructured.NestedSlice(
		sidecar.Object, "spec", "containers",
	)
	deploymentContainers = append(deploymentContainers, sidecarContainers...)
	err = unstructured.SetNestedSlice(
		regionEnv.PodObject.Object, deploymentContainers, "spec", "template", "spec", "containers",
	)
	if err != nil {
		regionEnv.errf("Failed to add sidecar container. %s", err)
		return
	}

	// Append volume to pod object.
	deploymentVolumes, _, _ := unstructured.NestedSlice(
		regionEnv.PodObject.Object, "spec", "template", "spec", "volumes",
	)
	sidecarVolumes, _, _ := unstructured.NestedSlice(
		sidecar.Object, "spec", "volumes",
	)
	deploymentVolumes = append(deploymentVolumes, sidecarVolumes...)
	err = unstructured.SetNestedSlice(
		regionEnv.PodObject.Object, deploymentVolumes, "spec", "template", "spec", "volumes",
	)
	if err != nil {
		regionEnv.errf("Failed to add sidecar volume. %s", err)
		return
	}

	regionEnv.Logger.Infof("Sidecar added to %s", regionEnv.PodObject.GetName())
}

func (regionEnv *RegionEnv) createObjectSecrets(object *unstructured.Unstructured, attempt int) {
	secretName := strings.ToLower(fmt.Sprintf("%s-%s", object.GetName(), object.GetKind()))
	paths, ok := regionEnv.ObjectRefs[secretName]
	regionEnv.Logger.Infof("Object Refs %+v, secretName %s", regionEnv.ObjectRefs, secretName)
	if !ok {
		regionEnv.Logger.Infof("No Objects Paths for %s %s", object.GetName(), object.GetKind())
		return
	}

	secretData := map[string]interface{}{}

	for _, path := range paths {
		query, err := gojq.Parse(path)
		if err != nil {
			regionEnv.errf("Bad path in object reference %s %s", secretName, path)
			regionEnv.errf(err.Error())
			return
		}
		iter := query.RunWithContext(regionEnv.Context, object.Object)
		valueInterface, ok := iter.Next()
		value := fmt.Sprintf("%s", valueInterface)
		if !ok || strings.Contains(value, "not defined") || strings.Contains(value, "cannot iterate") {
			// Object data may take time to populate.
			if attempt == 0 {
				sleep := 30 * time.Second
				regionEnv.Logger.Infof("Waiting %s for object path %s to populate", sleep, path)
				time.Sleep(sleep)
				regionEnv.createObjectSecrets(object, 1)
			} else {
				regionEnv.errf("Path in object reference %s %s not found", secretName, path)
			}
			return
		}
		duplicateValue, ok := iter.Next()
		if ok {
			regionEnv.errf("Multiple values found for %s at %s. Path must point to unique value. Found %s and %s",
				secretName, path, value, duplicateValue)
			return
		}
		// the path is the only unique key available. It's messy, but the secret is transparent to the user.
		// Remove forbidden characters
		key := removeInvalidCharacters(path)

		regionEnv.Logger.Infof("Path %+v", path)
		secretData[key] = base64.StdEncoding.EncodeToString([]byte(value))
	}

	secret := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name": secretName,
				"labels": map[string]interface{}{
					"app": regionEnv.PodObject.GetName(),
				},
			},
			"data": secretData,
		},
	}

	objectStream := make(chan *unstructured.Unstructured)
	defer close(objectStream)
	go dynamicUpdate(nil, &secret, regionEnv, objectStream)
	newSecret := <-objectStream

	regionEnv.Logger.Infof("Created Secret %s from Object %s", secretName, object.GetName())
	regionEnv.Logger.Debugf("%+v", newSecret)
}
