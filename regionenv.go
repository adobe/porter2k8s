package porter2k8s

import (
	"container/heap"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	catalogv1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	catalogclientset "github.com/kubernetes-sigs/service-catalog/pkg/client/clientset_generated/clientset"
	contourv1beta1 "github.com/projectcontour/contour/apis/contour/v1beta1"
	contourversioned "github.com/projectcontour/contour/apis/generated/clientset/versioned"
	log "github.com/sirupsen/logrus"
	istiov1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	istioversioned "istio.io/client-go/pkg/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	autoscaling "k8s.io/api/autoscaling/v2beta2"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	policy "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// RegionEnv is the cluster/region specific configuration.
type RegionEnv struct {
	CatalogClientset    catalogclientset.Interface
	Cfg                 *CmdConfig
	Clientset           kubernetes.Interface // kubernetes.Clientset or testclient.Clientset
	ClusterSettings     map[string]string    // Settings for porter2k8s.
	ConfigMap           *v1.ConfigMap
	ContourClientset    contourversioned.Interface
	Deployment          *appsv1.Deployment
	Job                 *batchv1.Job
	Context             context.Context
	Errors              []error
	Gateway             *istiov1beta1.Gateway
	HPAutoscaler        *autoscaling.HorizontalPodAutoscaler
	Ingress             *extensionsv1beta1.Ingress
	IngressRouteList    *contourv1beta1.IngressRouteList
	IstioClientset      istioversioned.Interface
	NamedSecrets        *v1.SecretList // Secret objects defined by service teams
	PodDisruptionBudget *policy.PodDisruptionBudget
	Region              string
	SecretKeyRefs       []SecretKeyRef
	Secrets             []SecretRef
	Service             *v1.Service
	ServiceAccount      *v1.ServiceAccount
	ServiceBindings     map[string][]*catalogv1beta1.ServiceBinding  // Open Service Broker Service Bindings.
	ServiceInstances    map[string][]*catalogv1beta1.ServiceInstance // Open Service Broker Service Instances.
	Vars                []EnvVar
	VirtualService      *istiov1beta1.VirtualService
}

// RegionEnv Methods

// deleteJob deletes the specified job on K8s cluster.
// if wait is true, blocks until job deletion is completed
// if wait is false, returns once the cluster has received the delete request; the operation may still be in progress
func (regionEnv *RegionEnv) deleteJob(job *batchv1.Job, wait bool) error {
	jobInterface := regionEnv.Clientset.BatchV1().Jobs(regionEnv.Cfg.Namespace)

	propagation := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: int64Ref(0),
		PropagationPolicy:  &propagation,
	}
	err := jobInterface.Delete(job.Name, &deleteOptions)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if !wait {
		return nil
	}
	listOptions := metav1.ListOptions{
		FieldSelector:   fmt.Sprintf("metadata.name=%s", job.Name),
		ResourceVersion: job.ResourceVersion,
		TimeoutSeconds:  int64Ref(30),
	}
	watcher, err := jobInterface.Watch(listOptions)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	watchCh := watcher.ResultChan()
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
				log.Warnf("error reported during job deletion: %v", event.Object)
			}
		}
	}
}

// deleteOldConfigMaps keeps only the newest (maxConfigMaps) number of configmaps for the given application, with the
// exception of that being used by the currently running container, which was created by the previous successful
// deployment.
func (regionEnv *RegionEnv) deleteOldConfigMaps(deploymentName string, protectedSha map[string]bool) bool {
	configMapInterface := regionEnv.Clientset.CoreV1().ConfigMaps(regionEnv.Cfg.Namespace)
	listOptions := metav1.ListOptions{
		LabelSelector:  fmt.Sprintf("app=%s", deploymentName),
		TimeoutSeconds: int64Ref(30),
	}
	deleteOptions := metav1.DeleteOptions{}
	currentCMs, listErr := configMapInterface.List(listOptions)
	if listErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Error listing configmaps:\n%v", listErr))
		return false
	}

	// creationDates holds creation times for heap.
	//var creationDates [len(currentCMs.Items)]TimeHeap
	if len(currentCMs.Items) < regionEnv.Cfg.MaxConfigMaps {
		return true
	}
	// Delete old ConfigMaps until we have fewer than what is desired.
	log.Infof("Number of configmaps for application exceed limit of %d", regionEnv.Cfg.MaxConfigMaps)
	var currentCMHeap ConfigMapHeap = currentCMs.Items
	heap.Init(&currentCMHeap)
	for len(currentCMHeap) > regionEnv.Cfg.MaxConfigMaps {
		oldCM := heap.Pop(&currentCMHeap).(v1.ConfigMap)
		if _, ok := protectedSha[oldCM.ObjectMeta.Labels["sha"]]; ok {
			log.Infof("Not deleting configmap %s as it is a protected SHA", oldCM.ObjectMeta.Name)
		} else if strings.Contains(oldCM.ObjectMeta.Name, "fluent-bit-config") {
			log.Infof("Not deleting configmap %s as it is the logging sidecar configuration", oldCM.ObjectMeta.Name)
		} else {
			log.Infof("Deleting old configmap %s", oldCM.ObjectMeta.Name)
			deleteErr := configMapInterface.Delete(oldCM.ObjectMeta.Name, &deleteOptions)
			if deleteErr != nil {
				regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Error deleting configmaps:\n%v", deleteErr))
				return false
			}
		}
	}
	return true
}

// deleteOldSecrets keeps only the newest (maxSecrets) number of secrets for the given application, with the
// exception of that being used by the currently running container, which was created by the previous successful
// deployment.
func (regionEnv *RegionEnv) deleteOldSecrets(deploymentName string, protectedSha map[string]bool) bool {
	secretInterface := regionEnv.Clientset.CoreV1().Secrets(regionEnv.Cfg.Namespace)
	listOptions := metav1.ListOptions{
		LabelSelector:  fmt.Sprintf("app=%s", deploymentName),
		TimeoutSeconds: int64Ref(30),
	}
	deleteOptions := metav1.DeleteOptions{}
	currentCMs, listErr := secretInterface.List(listOptions)
	if listErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Error listing secrets:\n%v", listErr))
		return false
	}

	// creationDates holds creation times for heap.
	//var creationDates [len(currentCMs.Items)]TimeHeap
	if len(currentCMs.Items) < regionEnv.Cfg.MaxConfigMaps {
		return true
	}
	// Delete old Secrets until we have fewer than what is desired.
	log.Infof("Number of secrets for application exceed limit of %d", regionEnv.Cfg.MaxConfigMaps)
	var currentCMHeap SecretHeap = currentCMs.Items
	heap.Init(&currentCMHeap)
	for len(currentCMHeap) > regionEnv.Cfg.MaxConfigMaps {
		oldCM := heap.Pop(&currentCMHeap).(v1.Secret)
		if _, ok := protectedSha[oldCM.ObjectMeta.Labels["sha"]]; ok {
			log.Infof("Not deleting secret %s as it is a protected SHA", oldCM.ObjectMeta.Name)
		} else {
			log.Infof("Deleting old secret %s", oldCM.ObjectMeta.Name)
			deleteErr := secretInterface.Delete(oldCM.ObjectMeta.Name, &deleteOptions)
			if deleteErr != nil {
				regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Error deleting secrets:\n%v", deleteErr))
				return false
			}
		}
	}
	return true
}

// replaceDeploymentAnnotations replaces the tokens in the annotations with cluster specific values.
// This feature is implemented with kube2iam in mind.
func (regionEnv *RegionEnv) replaceDeploymentAnnotations(kubeObject metav1.Object) {
	// Iterate through annotations in Deployment containers.
	podTemplateSpec := findPodTemplateSpec(kubeObject)
	for key, value := range podTemplateSpec.ObjectMeta.Annotations {
		podTemplateSpec.ObjectMeta.Annotations[key] = tokenReplaceString(value, regionEnv.ClusterSettings)
	}
}

// updateServiceInstanceParameters replaces the tokens in the annotations with cluster specific values.
// This feature is implemented with kube2iam in mind.
func (regionEnv *RegionEnv) replaceServiceInstanceParameters(cloud string) {
	// Iterate through annotations in Service Instance spec.
	for _, serviceInstance := range regionEnv.ServiceInstances[cloud] {
		serviceInstance.Spec.Parameters.Raw =
			tokenReplace(serviceInstance.Spec.Parameters.Raw, regionEnv.ClusterSettings)
	}
}

// replaceNamedSecretData replaces the tokens in the data field with cluster specific values.
func (regionEnv *RegionEnv) replaceNamedSecretData() {
	// Iterate through Secret Data and replace values
	for _, namedSecret := range regionEnv.NamedSecrets.Items {
		for key, value := range namedSecret.StringData {
			log.Debugf("replaceNamedSecretData: original key/values [%s: %s]\n", key, value)
			// Checks for non-placeholder values.  Static secret assignments are not allowed.
			// All values must be placeholders that are replaced after being retrieved from Vault
			if strings.Contains(value, "{{") && strings.Contains(value, "}}") {
				namedSecret.StringData[key] = tokenReplaceString(value, regionEnv.ClusterSettings)
				// To prevent leaking secrets to logs, do not remove the following line comment in production
				// log.Debugf("replaceNamedSecretData: new key/values [%s: %s]", key, namedSecret.StringData[key])
			} else {
				regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Setting static secret data in "+
					"key %s is forbidden.  You must store secret values in Vault and insert them "+
					"using placeholders.", key))
			}
		}
	}
}

// updateGateway replaces the gateway of the Istio Virtual service if the "GATEWAY" key exists within
// the ClusterSettings.
func (regionEnv *RegionEnv) updateGateway() {
	gateways, ok := regionEnv.ClusterSettings["GATEWAYS"]
	if !ok {
		log.Infof(
			"No local gateway found for %s.\nContinuing with %s.",
			regionEnv.Region,
			regionEnv.VirtualService.Spec.Gateways,
		)
		return
	}
	gatewaySlice := strings.Fields(gateways)
	regionEnv.VirtualService.Spec.Gateways = gatewaySlice
	log.Infof("Changed gateways to %s", gatewaySlice)
}

// updateRegistry replaces the registry of docker image within deployment if the "REGISTRY" key exists within
// the ClusterSettings.
func (regionEnv *RegionEnv) updateRegistry(kubeObject metav1.Object) {
	registry, ok := regionEnv.ClusterSettings["REGISTRY"]
	if !ok {
		log.Infof("No local registry found for %s.", regionEnv.Region)
		return
	}
	// Replace registry in the docker image, by substituting text up to the first slash.
	// Ex. 983073263818.dkr.ecr.us-west-2.amazonaws.com/echosign/background-removal ->
	// 983073263818.dkr.ecr.us-east-1.amazonaws.com/echosign/background-removal
	registry = fmt.Sprintf("%s/$1", registry)
	regex := regexp.MustCompile(`.*?/(.*)`)
	applicationContainer := findContainer(kubeObject)
	if applicationContainer == nil {
		log.Errorf("Application container not found! Region: %s", regionEnv.Region)
		return
	}
	applicationContainer.Image = regex.ReplaceAllString(applicationContainer.Image, registry)
	log.Infof("Changed image to %s", applicationContainer.Image)
}

// updateDomainName replaces the domain name of the object with the cluster value if the "DOMAIN" key exists within
// the ClusterSettings. This assumes that all domain names need to be changed to a different value.
// This may need to be smarter in the future.
func (regionEnv *RegionEnv) updateDomainName() {
	domainList, ok := regionEnv.ClusterSettings["DOMAIN"]
	if !ok {
		log.Infof("No local domain name found for %s.", regionEnv.Region)
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
	log.Infof("Domains: %s", domains)

	if regionEnv.Ingress != nil {
		// Replace rules.
		var newRules []extensionsv1beta1.IngressRule
		for _, ingressRule := range regionEnv.Ingress.Spec.Rules {
			for _, domain := range domains {
				var newRule extensionsv1beta1.IngressRule
				newRule.Host = regex.ReplaceAllString(ingressRule.Host, domain)
				newRules = append(newRules, newRule)
				log.Infof("Changed Ingress domain name to %s", newRule.Host)
			}
		}
		regionEnv.Ingress.Spec.Rules = newRules

		// Replace TLS
		for i, ingressTLS := range regionEnv.Ingress.Spec.TLS {
			hosts := ingressTLS.Hosts
			var newHosts []string
			for _, host := range hosts {
				for _, domain := range domains {
					newHosts = append(newHosts, regex.ReplaceAllString(host, domain))
				}
			}
			log.Infof("Changed Ingress TLS domain to %s", newHosts)
			regionEnv.Ingress.Spec.TLS[i].Hosts = newHosts
		}
	}

	// IngressRoutes have at most one virtualhost, so if multiple domains are given,
	// a new IngressRoute has to be created for each one.  The domain replacements
	// are determined based on the contour annotation "kubernetes.io/ingress.class".
	// For ethos-k8s, the DOMAIN setting is that of the ethos cluster:  <cluster-name>.ethos.adobe.net
	// Ingress classes of contour-internal and contour-corp have an additional domain component
	// of "int" or "corp" inserted between the service name and the cluster domain.
	// https://git.corp.adobe.com/adobe-platform/k8s-infrastructure/blob/master/docs/user-guide.md#ingress-dns-names

	if regionEnv.IngressRouteList != nil {
		var newIngressRouteList *contourv1beta1.IngressRouteList = new(contourv1beta1.IngressRouteList)
		var clusterDomain string
		//		cluster_regex := regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9-_]+\.[a-zA-Z0-9][a-zA-Z0-9-_]+)\.*`)
		if len(domains) > 0 && domains[0] != "" {
			clusterDomain = domains[0]
			for _, newIngressRoute := range regionEnv.IngressRouteList.Items {
				switch newIngressRoute.ObjectMeta.Annotations["kubernetes.io/ingress.class"] {
				case "contour-corp":
					// prefix base cluster domain with "corp", per ethos cluster domain convention:
					newCorpDomain := insertDomainPart(clusterDomain, 1, "corp")
					newIngressRoute.Spec.VirtualHost.Fqdn = regex.ReplaceAllString(
						newIngressRoute.Spec.VirtualHost.Fqdn,
						newCorpDomain,
					)
					// Sanity check the SecretName if TLS is in use
					if newIngressRoute.Spec.VirtualHost.TLS != nil &&
						newIngressRoute.Spec.VirtualHost.TLS.SecretName != "heptio-contour/cluster-ssl-corp" {
						log.Errorf(
							"Invalid IngressRoute class + TLS SecretName combination: contour-corp + %s",
							newIngressRoute.Spec.VirtualHost.TLS.SecretName,
						)
					}
				case "contour-internal":
					// prefix base cluster domain with "int"
					newInternalDomain := insertDomainPart(clusterDomain, 1, "int")
					newIngressRoute.Spec.VirtualHost.Fqdn = regex.ReplaceAllString(
						newIngressRoute.Spec.VirtualHost.Fqdn,
						newInternalDomain,
					)
					// Sanity check the SecretName if TLS is in use
					if newIngressRoute.Spec.VirtualHost.TLS != nil &&
						newIngressRoute.Spec.VirtualHost.TLS.SecretName != "heptio-contour/cluster-ssl-int" {
						log.Errorf(
							"Invalid IngressRoute class + TLS SecretName combination: contour-internal + %s",
							newIngressRoute.Spec.VirtualHost.TLS.SecretName,
						)
					}
				case "contour-public":
					// replace the fqdn only if the cluster TLS cert is in use
					if newIngressRoute.Spec.VirtualHost.TLS != nil &&
						newIngressRoute.Spec.VirtualHost.TLS.SecretName == "heptio-contour/cluster-ssl-public" {
						newIngressRoute.Spec.VirtualHost.Fqdn = regex.ReplaceAllString(
							newIngressRoute.Spec.VirtualHost.Fqdn,
							clusterDomain,
						)
					}
				default:
					log.Errorf(
						"Invalid kubernetes.io/ingress.class:  %s",
						newIngressRoute.ObjectMeta.Annotations["kubernetes.io/ingress.class"],
					)
				}
				newIngressRouteList.Items = append(newIngressRouteList.Items, newIngressRoute)
				log.Infof("Added IngressRoute for fqdn: %s", newIngressRoute.Spec.VirtualHost.Fqdn)
			}
			regionEnv.IngressRouteList = newIngressRouteList
		}
	}

	if regionEnv.Gateway != nil {
		for i, server := range regionEnv.Gateway.Spec.Servers {
			var newHosts []string
			for _, host := range server.Hosts {
				for _, domain := range domains {
					newHosts = append(newHosts, regex.ReplaceAllString(host, domain))
				}
			}
			log.Infof("Changed Gateway domain to %s", newHosts)
			regionEnv.Gateway.Spec.Servers[i].Hosts = newHosts
		}
	}
	if regionEnv.VirtualService != nil {
		var newHosts []string
		for _, host := range regionEnv.VirtualService.Spec.Hosts {
			for _, domain := range domains {
				newHosts = append(newHosts, regex.ReplaceAllString(host, domain))
			}
		}
		log.Infof("Changed Virtual Service domain to %s", newHosts)
		regionEnv.VirtualService.Spec.Hosts = newHosts
	}
}

// createConfigMap creates the deployment configmap.
func (regionEnv *RegionEnv) createConfigMap(deploymentName string) bool {
	configMapInterface := regionEnv.Clientset.CoreV1().ConfigMaps(regionEnv.Cfg.Namespace)
	// Use Kubernetes labels to easily find and delete old configmaps.
	configMap := v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", deploymentName, regionEnv.Cfg.SHA),
			Labels: map[string]string{
				"app": deploymentName,
				"sha": regionEnv.Cfg.SHA,
			},
		},
	}
	data := make(map[string]string)
	for _, envVar := range regionEnv.Vars {
		data[envVar.Name] = envVar.Value
	}
	configMap.Data = data

	_, configMapGetErr := configMapInterface.Get(configMap.ObjectMeta.Name, metav1.GetOptions{})

	// ConfigMap may already exist if this is a rollback.
	if configMapGetErr == nil {
		log.Info("Config Map already exists")
		return true
	}
	if !errors.IsNotFound(configMapGetErr) {
		regionEnv.Errors = append(regionEnv.Errors, configMapGetErr)
		log.Infof("Unexpected ConfigMap get error\n%s", configMapGetErr.Error())
		return false
	}

	configMapUploaded, configMapErr := configMapInterface.Create(&configMap)
	if configMapErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, configMapErr)
		return false
	}
	log.Infof("Created Config Map\n%+v", configMapUploaded)

	return true
}

// createSecret creates the deployment secret.
func (regionEnv *RegionEnv) createSecret(deploymentName string) bool {
	secretInterface := regionEnv.Clientset.CoreV1().Secrets(regionEnv.Cfg.Namespace)
	// Use Kubernetes labels to easily find and delete old secrets.
	secret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", deploymentName, regionEnv.Cfg.SHA),
			Labels: map[string]string{
				"app": deploymentName,
				"sha": regionEnv.Cfg.SHA,
			},
		}}
	secretData := make(map[string][]byte)
	for _, secretRef := range regionEnv.Secrets {
		if secretRef.Kind == "container" {
			secretData[secretRef.Name] = []byte(secretRef.Value)
		}
	}
	secret.Data = secretData

	_, secretGetErr := secretInterface.Get(secret.ObjectMeta.Name, metav1.GetOptions{})

	// Secret may already exist if this is a rollback.
	if secretGetErr == nil {
		log.Info("Secret already exists")
		return true
	}
	if !errors.IsNotFound(secretGetErr) {
		regionEnv.Errors = append(regionEnv.Errors, secretGetErr)
		log.Infof("Unexpected Secret get error\n%s", secretGetErr.Error())
		return false
	}

	_, secretErr := secretInterface.Create(&secret)
	if secretErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, secretErr)
		return false
	}
	log.Infof("Created Secret\n%s", secret.ObjectMeta.Name)

	return true
}

// manualSidecarInjectionRequired returns true if porter2k8s must inject the istio sidecar.
// The sidecar must be injected if Istio is in use but the namespace does not have the label 'istio-injection=enabled'.
func (regionEnv *RegionEnv) manualSidecarInjectionRequired() bool {
	// Assume istio is not in use if not in the porter2k8s configMap.
	if regionEnv.ClusterSettings["ISTIO"] != "true" {
		return false
	}
	// Determine if automatic sidecar injection is enabled.
	namespaceInterface := regionEnv.Clientset.CoreV1().Namespaces()
	namespace, getErr := namespaceInterface.Get(regionEnv.Cfg.Namespace, metav1.GetOptions{})
	if getErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get current namespace.\n%s", getErr))
	}
	if namespace.ObjectMeta.Labels["istio-injection"] == "enabled" {
		return false
	}
	return true
}

// updateHPAMin replaces the minimum Horizontal Pod Autoscaler value with cluster specific values.
func (regionEnv *RegionEnv) updateHPAMinimum() {
	clusterMin, ok := regionEnv.ClusterSettings["HPAMIN"]
	if !ok {
		log.Infof(
			"No local horizontal pod autoscaler minimum value found for %s.\nContinuing with %d.",
			regionEnv.Region,
			*regionEnv.HPAutoscaler.Spec.MinReplicas,
		)
		return
	}
	clusterMinRef := int32Ref(clusterMin)
	if *regionEnv.HPAutoscaler.Spec.MinReplicas < *clusterMinRef {
		regionEnv.HPAutoscaler.Spec.MinReplicas = clusterMinRef
	}
	if regionEnv.HPAutoscaler.Spec.MaxReplicas < *clusterMinRef {
		regionEnv.HPAutoscaler.Spec.MaxReplicas = *clusterMinRef
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

// Inject secret key references into container.
func (regionEnv *RegionEnv) injectSecretKeyRefs(kubeObject metav1.Object) error {
	applicationContainer := findContainer(kubeObject)
	if applicationContainer == nil {
		return fmt.Errorf("Unable to find application image in deployment spec")
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
	return nil
}

// Inject secret from secret binding into deployment.
func (regionEnv *RegionEnv) postDeployCleanup(previousDeployment *appsv1.Deployment) error {
	deploymentName := regionEnv.Deployment.ObjectMeta.Name
	// Do not delete configmaps or secret objects associated with the current or previously deployed sha.
	protectedShas := map[string]bool{
		previousDeployment.ObjectMeta.Labels["sha"]: true,
		regionEnv.Cfg.SHA: true,
	}
	// Delete old configmaps.
	configMapDeleteSucceeded := regionEnv.deleteOldConfigMaps(deploymentName, protectedShas)
	if !configMapDeleteSucceeded {
		return fmt.Errorf("exiting on configmap deletion error")
	}
	// Delete old secrets.
	secretDeleteSucceeded := regionEnv.deleteOldSecrets(deploymentName, protectedShas)
	if !secretDeleteSucceeded {
		return fmt.Errorf("exiting on secret deletion failure")
	}
	return nil
}

// porter2k8sConfigMap fetches cluster specific porter2k8s configmap, if it exists.
func (regionEnv *RegionEnv) porter2k8sConfigMap() {
	configMapInterface := regionEnv.Clientset.CoreV1().ConfigMaps(regionEnv.Cfg.Namespace)
	configMap, configMapErr := configMapInterface.Get("porter2k8s", metav1.GetOptions{})
	if configMapErr == nil {
		// Overwrite cluster settings from the namespace with those from the service's configuration.
		for key, value := range regionEnv.ClusterSettings {
			configMap.Data[key] = value
		}
		regionEnv.ClusterSettings = configMap.Data
		return
	} else if errors.IsNotFound(configMapErr) {
		log.Info("No cluster specific porter2k8s config found.")
	} else {
		log.Errorf("unexpected Error retrieving porter2k8s configMap.\n%v", configMapErr)
	}
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

// watchDeployment until first set of pods in deployment is ready.
func (regionEnv *RegionEnv) watchDeployment(deployment *appsv1.Deployment) (bool, error) {
	deploymentInterface := regionEnv.Clientset.AppsV1().Deployments(regionEnv.Cfg.Namespace)
	var deploymentUpdate *appsv1.Deployment

	podLogger, err := StreamPodLogs(
		regionEnv.Context,
		regionEnv.Clientset.CoreV1().Pods(regionEnv.Cfg.Namespace),
		fmt.Sprintf("app=%s,sha=%s", deployment.ObjectMeta.Name, deployment.ObjectMeta.Labels["sha"]),
		deployment.Name,
		regionEnv.Cfg.PodLogSinker)
	if err != nil {
		return false, err
	}
	defer podLogger.Stop(1 * time.Second) // cap pod log streaming 1 second after all replicas came up or failed

	listOptions := metav1.ListOptions{
		LabelSelector:  fmt.Sprintf("app=%s,sha=%s", deployment.ObjectMeta.Name, deployment.ObjectMeta.Labels["sha"]),
		TimeoutSeconds: calculateWatchTimeout(deployment, int32(regionEnv.Cfg.Wait)),
	}
	log.Infof("Waiting %d seconds for deployment to complete in %s", *listOptions.TimeoutSeconds, regionEnv.Region)

	watcher, watchErr := deploymentInterface.Watch(listOptions)
	if watchErr != nil {
		return false, watchErr
	}
	defer watcher.Stop()

	watchCh := watcher.ResultChan()
	for {
		select {
		case <-regionEnv.Context.Done():
			log.Info("Received Done")
			// Returning true to prevent an error from being reported.
			// If SIGTERM was sent, the user doesn't care about the deployment status.
			return true, nil

		case event, ok := <-watchCh:
			if !ok {
				return false, fmt.Errorf("timeout watching deployment")
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				if deploymentUpdate, ok = event.Object.(*appsv1.Deployment); !ok {
					// The watch has failed, which means something went wrong.
					// Just get the status of the deployment and exit.
					deploymentUpdate, getErr := deploymentInterface.Get(deployment.ObjectMeta.Name, metav1.GetOptions{})
					if getErr != nil {
						watchErr = fmt.Errorf("Status: %+v", deploymentUpdate.Status)
					} else {
						watchErr = fmt.Errorf("Deployment failed: Check application logs above for more details")
					}
					return false, watchErr
				}
				log.Infof("replicas: %d, updatedReplicas: %d, readyReplicas: %d, availableReplicas: %d, unavailableReplicas: %d",
					deploymentUpdate.Status.Replicas, deploymentUpdate.Status.UpdatedReplicas, deploymentUpdate.Status.ReadyReplicas,
					deploymentUpdate.Status.AvailableReplicas, deploymentUpdate.Status.UnavailableReplicas)
				if latestCondition := len(deploymentUpdate.Status.Conditions) - 1; latestCondition >= 0 {
					log.Infof("status: %s, message: %s",
						deploymentUpdate.Status.Conditions[latestCondition].Reason,
						deploymentUpdate.Status.Conditions[latestCondition].Message)
				}
				log.Debugf("Deployment Status %s: %+v", regionEnv.Region, deploymentUpdate.Status)
				// Unavailable Replicas:
				// Total number of unavailable pods targeted by this deployment. This is the total number of
				// pods that are still required for the deployment to have 100% available capacity. They may
				// either be pods that are running but not yet available or pods that still have not been created.
				if deploymentUpdate.Status.UnavailableReplicas == 0 && deploymentUpdate.Status.AvailableReplicas != 0 {
					log.Infof("Deployment Complete in %s", regionEnv.Region)
					return true, nil
				}

			case watch.Deleted:
				// someone deleted our deployment!!
				return false, fmt.Errorf("Deployment deleted while watching for completion")

			case watch.Error:
				err := errors.FromObject(event.Object)
				log.Warnf("error reported while watching deployment: %v", err)
			}
		}
	}
}

// watchServiceInstance until first set of pods in serviceInstance is ready.
func (regionEnv *RegionEnv) watchServiceInstance(serviceInstance *catalogv1beta1.ServiceInstance) (bool, error) {
	var ok bool
	var serviceInstanceUpdate *catalogv1beta1.ServiceInstance
	name := serviceInstance.ObjectMeta.Name
	listOptions := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String(),
		// Twenty times the wait as used for the deployment. RDS replication instances can take well over an hour.
		TimeoutSeconds: int64Ref(20 * regionEnv.Cfg.Wait),
	}
	log.Infof("Waiting %d seconds for serviceInstance to be ready in %s", *listOptions.TimeoutSeconds, regionEnv.Region)

	serviceInstanceInterface := regionEnv.CatalogClientset.ServicecatalogV1beta1().ServiceInstances(
		regionEnv.Cfg.Namespace)
	watcher, watchErr := serviceInstanceInterface.Watch(listOptions)
	if watchErr != nil {
		return false, watchErr
	}
	watchCh := watcher.ResultChan()
	for {
		select {
		case <-regionEnv.Context.Done():
			log.Info("Received Done")
			// Returning true to prevent an error from being reported.
			// If SIGTERM was sent, the user doesn't care about the serviceInstance status.
			return true, nil
		case event := <-watchCh:
			if serviceInstanceUpdate, ok = event.Object.(*catalogv1beta1.ServiceInstance); !ok {
				watchErr = fmt.Errorf("unexpected type returned in %s\n%+v", regionEnv.Region, event)
				watcher.Stop()
				return false, watchErr
			}
			log.Debugf("Service instance Status %s: %+v", regionEnv.Region, serviceInstanceUpdate.Status)
			for _, condition := range serviceInstanceUpdate.Status.Conditions {
				if condition.Type == catalogv1beta1.ServiceInstanceConditionFailed ||
					condition.Type == catalogv1beta1.ServiceInstanceConditionOrphanMitigation &&
						condition.Status == catalogv1beta1.ConditionTrue {
					watchErr = fmt.Errorf("%s: %s", condition.Reason, condition.Message)
					return false, watchErr
				}
				if condition.Type == catalogv1beta1.ServiceInstanceConditionReady &&
					condition.Status == catalogv1beta1.ConditionTrue {
					log.Infof("Service Instance ready in %s", regionEnv.Region)
					watcher.Stop()
					return true, nil
				}
				log.Debugf("Service Instance provisioning in %s, condition: %s", regionEnv.Region, condition)
			}
		}
	}
	return false, nil
}

func (regionEnv *RegionEnv) watchServiceBinding(serviceBinding *catalogv1beta1.ServiceBinding) (bool, error) {
	var serviceBindingUpdate *catalogv1beta1.ServiceBinding
	name := serviceBinding.ObjectMeta.Name
	listOptions := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String(),
		// 30 seconds should be plenty since bindings are a lightweight resource
		TimeoutSeconds: int64Ref(30),
	}
	log.Infof("Waiting %d seconds for serviceBinding to be ready in %s", *listOptions.TimeoutSeconds, regionEnv.Region)

	serviceBindingInterface := regionEnv.CatalogClientset.ServicecatalogV1beta1().ServiceBindings(
		regionEnv.Cfg.Namespace)
	watcher, watchErr := serviceBindingInterface.Watch(listOptions)
	if watchErr != nil {
		return false, watchErr
	}
	defer watcher.Stop()

	watchCh := watcher.ResultChan()
	for {
		select {
		case <-regionEnv.Context.Done():
			return false, regionEnv.Context.Err()

		case event, ok := <-watchCh:
			if !ok {
				return false, fmt.Errorf("timeout watching serviceBinding")
			}

			switch event.Type {
			case watch.Added, watch.Modified:

				if serviceBindingUpdate, ok = event.Object.(*catalogv1beta1.ServiceBinding); !ok {
					watchErr = fmt.Errorf("unexpected type returned in %s\n%+v", regionEnv.Region, event)
					return false, watchErr
				}
				log.Debugf("ServiceBinding Status %s: %+v", regionEnv.Region, serviceBindingUpdate.Status)

				if latestCondition := len(serviceBindingUpdate.Status.Conditions) - 1; latestCondition >= 0 {
					condition := serviceBindingUpdate.Status.Conditions[latestCondition]

					if condition.Type == catalogv1beta1.ServiceBindingConditionFailed &&
						condition.Status == catalogv1beta1.ConditionTrue {
						watchErr = fmt.Errorf("%s: %s", condition.Reason, condition.Message)
						return false, watchErr
					}

					if condition.Type == catalogv1beta1.ServiceBindingConditionReady &&
						condition.Status == catalogv1beta1.ConditionTrue {
						log.Infof("Service Binding ready in %s", regionEnv.Region)
						return true, nil
					}
				}

			case watch.Deleted:
				// someone deleted our service binding!!
				return false, fmt.Errorf("ServiceBinding deleted while watching for completion")

			case watch.Error:
				err := errors.FromObject(event.Object)
				log.Warnf("error reported while watching serviceBinding: %v", err)
			}
		}
	}
	return false, nil
}

// watchJob until completed or failed.
func (regionEnv *RegionEnv) watchJob(job *batchv1.Job) (*batchv1.Job, error) {
	client := regionEnv.Clientset.BatchV1().Jobs(regionEnv.Cfg.Namespace)
	selector, err := metav1.LabelSelectorAsSelector(job.Spec.Selector)
	if err != nil {
		return job, err
	}

	podLogger, err := StreamPodLogs(
		regionEnv.Context,
		regionEnv.Clientset.CoreV1().Pods(regionEnv.Cfg.Namespace),
		selector.String(),
		job.Name,
		regionEnv.Cfg.PodLogSinker)
	if err != nil {
		return job, err
	}
	defer podLogger.Stop(5 * time.Second) // allow up to 5 seconds for job log streaming to complete

	listOptions := metav1.ListOptions{
		LabelSelector:  fmt.Sprintf("app=%s,sha=%s", job.ObjectMeta.Name, job.ObjectMeta.Labels["sha"]),
		TimeoutSeconds: int64Ref(regionEnv.Cfg.Wait),
	}
	log.Infof("Waiting %d seconds for job to complete in %s", *listOptions.TimeoutSeconds, regionEnv.Region)

	watcher, err := client.Watch(listOptions)
	if err != nil {
		return job, err
	}
	defer watcher.Stop()

	watchCh := watcher.ResultChan()
	for {
		select {
		case <-regionEnv.Context.Done():
			return job, regionEnv.Context.Err()

		case event, ok := <-watchCh:
			if !ok {
				return job, fmt.Errorf("timeout watching job")
			}

			switch event.Type {
			case watch.Added, watch.Modified, watch.Deleted:
				jobUpdate, ok := event.Object.(*batchv1.Job)
				if !ok {
					return job, fmt.Errorf("%#v is not a job event", event)
				}

				log.Infof("Pods Statuses: %d Running / %d Succeeded / %d Failed",
					jobUpdate.Status.Active, jobUpdate.Status.Succeeded, jobUpdate.Status.Failed)

				if len(jobUpdate.Status.Conditions) > 0 {
					return jobUpdate, nil
				}
				if event.Type == watch.Deleted {
					// someone deleted our job!!
					return jobUpdate, fmt.Errorf("Job deleted while watching for completion")
				}

			case watch.Error:
				err := errors.FromObject(event.Object)
				log.Warnf("error reported while watching job: %v", err)
			}
		}
	}
}
