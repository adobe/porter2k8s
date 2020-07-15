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

// Package porter2k8s parses a Porter configuration and deploys to kubernetes.
// It creates a deployment specific configuration map and secret object from the config.
// It then creates or updates the project's objects, and a deploys a sha specific image
// (created elsewhere) for the project which references the configmap and secret object.
// In the future Porter2k8s will also support more simplified configuration for services which no longer use Porter.
package porter2k8s

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	catalogv1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	catalogclientset "github.com/kubernetes-sigs/service-catalog/pkg/client/clientset_generated/clientset"
	contourv1beta1 "github.com/projectcontour/contour/apis/contour/v1beta1"
	contourversioned "github.com/projectcontour/contour/apis/generated/clientset/versioned"
	log "github.com/sirupsen/logrus"
	istioversioned "istio.io/client-go/pkg/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const clusterConfigFile = "cluster-config.yaml"

// CmdConfig is contains the parsed args.
type CmdConfig struct {
	ConfigPath          string
	ConfigType          string // Porter or Simple
	Environment         string
	MaxConfigMaps       int
	Namespace           string
	Regions             string
	SecretPathWhiteList string
	SHA                 string
	VaultAddress        string
	VaultBasePath       string // Vault base path for secrets.
	VaultToken          string
	Verbose             bool
	Wait                int
	LogMode             string
	LogDir              string
	PodLogSinker        PodLogSinker
}

// SecretRef is a reference to an individual secret in Vault.
type SecretRef struct {
	Key   string
	Name  string
	Path  string
	Value string
	Kind  string
}

// SecretKeyRef is a reference to a key within a Kubernetes secret.
type SecretKeyRef struct {
	Key    string
	Name   string
	Secret string
}

// EnvVar is an environment variable for the ConfigMap.
type EnvVar struct {
	Name  string
	Value string
}

type configReader interface {
	getEnv(*RegionEnv)
	parse(*RegionEnv)
}

// UpdateFn is a function that can update a single type of Kubernetes object in multiple clusters.
type UpdateFn func(chan interface{}, *sync.WaitGroup, *RegionEnv, chan *RegionEnv)

// Run is the main function.
func Run(args []string) {
	// Parse the arguments
	var cmdCfg CmdConfig
	if err := parse(args, &cmdCfg); err != nil {
		log.Errorf("%s", err)
		fmt.Fprintf(os.Stderr, "USAGE \n\n\t%s\n\n", os.Args[0])
		fmt.Fprint(os.Stderr, "GLOBAL OPTIONS:\n\n")
		usage(os.Stderr, &cmdCfg)

		os.Exit(1)
	}

	// set up global context for signal interuption
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stop = make(chan os.Signal)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-stop
		fmt.Printf("caught sig: %+v\n", sig)
		cancel()
		fmt.Println("Waiting up to 2 seconds to finish.")
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()

	// Global Configuration
	// Read deployment and service files into Kubernetes structs
	kubeServiceConfig, kubeServiceConfigErr := getServiceConfig(&cmdCfg)
	if kubeServiceConfigErr != nil {
		log.Errorf("%s", kubeServiceConfigErr)
		os.Exit(2)
	}

	// Regional configuration.
	// Gather environment variables and secret references.
	// Retrieve secrets from vault.
	// Create configmap and secret object.
	var regionEnvs []*RegionEnv
	for regionEnv := range createEnv(kubeServiceConfig, fetchSecrets(getConfig(ctx, &cmdCfg))) {
		log.Debugf("Retrieved Configuration %+v", regionEnv)
		if len(regionEnv.Errors) > 0 {
			log.Errorf("%s", regionEnv.Errors)
			os.Exit(2)
		}
		regionEnvs = append(regionEnvs, regionEnv)
	}

	// Run and monitor updates in this order.
	updateFns := []UpdateFn{
		updateConfigMapRegion,
		updateServiceRegion,
		updateServiceAccountRegion,
		updateIngressRegion,
		updateIngressRouteRegion,
		updateGatewayRegion,
		updateVirtualServiceRegion,
		updateServiceInstanceRegion,
		updateServiceBindingRegion,
		updateNamedSecretsRegion,
		updateDeploymentRegion,
		updateJobRegion,
		updateHPAutoscalerRegion,
		updatePodDisruptionBudgetRegion,
	}
	for _, updateFn := range updateFns {
		err := runUpdate(regionEnvs, updateFn)
		if err != nil {
			log.Errorf("%s", err)
			os.Exit(2)
		}
	}
}

// getServiceConfig reads the Kubernetes yaml files.
func getServiceConfig(cfg *CmdConfig) (KubeObjects, error) {
	// Intialize KubeObjects
	var kubeObjects KubeObjects
	kubeObjects.ServiceInstances = make(map[string][]*catalogv1beta1.ServiceInstance)
	kubeObjects.ServiceBindings = make(map[string][]*catalogv1beta1.ServiceBinding)
	kubeObjects.IngressRouteList = new(contourv1beta1.IngressRouteList)
	kubeObjects.NamedSecrets = new(v1.SecretList)

	// Allowed names for kubernetes object files are deployment.yaml, service.yaml, and ingress.yaml,
	// or their environment specific variations.
	// Istio CustomResourceDefinitions should be in istio.yaml or the environment specific variation.
	possibleFiles := []string{
		"deployment",
		"service",
		"ingress",
		"istio",
		"job",
		"ethos-logging-sidecar",
		"servicecatalog-aws",
		"servicecatalog-azure",
		"hpa",
		"pdb",
		"secret",
	}

	// Pull down kube configs from Vault.
	if err := kubeObjects.getKubeConfigs(cfg); err != nil {
		return kubeObjects, err
	}

	// Decode Kubernetes Objects from files.
	if err := kubeObjects.processObjects(cfg, possibleFiles); err != nil {
		return kubeObjects, err
	}

	// Perform checks/namespacing to ensure resources will not conflict with those of other services.
	if validationErr := kubeObjects.validateAndTag(); validationErr != nil {
		return kubeObjects, validationErr
	}

	deploymentErr := prepareDeployment(kubeObjects.parentObject(), cfg.SHA)
	return kubeObjects, deploymentErr
}

// getReader gets the config reader for the type of env var, secret reference listing.
func getReader(configType string) (configReader, error) {
	// Only one type of reader so far.
	if configType == "porter" {
		return &PorterEnv{}, nil
	}
	if configType == "simple" {
		return &SimpleEnv{}, nil
	}
	if configType == "test" {
		return &TestEnv{}, nil
	}
	return nil, fmt.Errorf("invalid config type %s", configType)
}

// getConfig fetches environment variables and secret references.
func getConfig(ctx context.Context, cfg *CmdConfig) <-chan *RegionEnv {
	envStream := make(chan *RegionEnv)
	go func() {
		defer log.Debug("Closing getConfig channel")
		defer close(envStream)

		for _, region := range strings.Fields(cfg.Regions) {
			serviceInstances := make(map[string][]*catalogv1beta1.ServiceInstance)
			serviceBindings := make(map[string][]*catalogv1beta1.ServiceBinding)
			// Create an environment for this region and populate it with information from the config files.
			environment := RegionEnv{
				Region:           region,
				Cfg:              cfg,
				Context:          ctx,
				ServiceInstances: serviceInstances,
				ServiceBindings:  serviceBindings,
			}

			reader, readerError := getReader(cfg.ConfigType)
			if readerError != nil {
				badEnv := RegionEnv{Errors: []error{readerError}}
				envStream <- &badEnv
				return
			}

			// getEnv adds values for the deployment specific configmap and secret.
			environment.ClusterSettings = map[string]string{}
			reader.getEnv(&environment)
			reader.parse(&environment)
			environment.identifySecrets()
			log.Infof("Created region specific configuration for %s.", region)
			select {
			case <-ctx.Done():
				log.Info("Received Done")
				return
			case envStream <- &environment:
			}
		}
	}()

	return envStream
}

// FetchSecrets from Vault.
func fetchSecrets(regionEnvStream <-chan *RegionEnv) <-chan *RegionEnv {
	envWithSecretsStream := make(chan *RegionEnv)
	go func() {
		defer log.Debug("Closing fetchSecrets channel")
		defer close(envWithSecretsStream)
		var client *vaultapi.Client
		var clientErr error
		// Cache secrets. Map of vaultapi.Secret.Data.
		secretCache := make(map[string]map[string]interface{})
		for regionEnv := range regionEnvStream {
			// Check for errors in the stream
			if len(regionEnv.Errors) > 0 {
				envWithSecretsStream <- regionEnv
				return
			}

			if client == nil {
				client, clientErr = newVaultClient(regionEnv.Cfg.VaultAddress, regionEnv.Cfg.VaultToken)
			}
			if clientErr != nil {
				regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf(
					"vault client initialization error %s",
					clientErr,
				))
				envWithSecretsStream <- regionEnv
				return
			}

			var secrets []SecretRef
			for _, secret := range regionEnv.Secrets {
				// Keys are optional in simple configs and do not exist in porter configs.
				if secret.Key == "" {
					secret.Key = secret.Name
				}
				vaultSecretData, ok := secretCache[secret.Path]
				if !ok {
					vaultSecret, readErr := client.Logical().Read(secret.Path)
					if readErr != nil {
						newErr := fmt.Errorf(
							"unable to retrieve secrets for region %s\n%v", regionEnv.Region,
							readErr,
						)
						regionEnv.Errors = append(regionEnv.Errors, newErr)
						continue
					}
					if vaultSecret == nil || vaultSecret.Data[secret.Key] == nil {
						newErr := fmt.Errorf(
							"vault path %s is valid but value for %s was not found",
							secret.Path,
							secret.Key,
						)
						regionEnv.Errors = append(regionEnv.Errors, newErr)
						continue
					}
					vaultSecretData = vaultSecret.Data
				}
				// Catch keys which do not exist in cache.
				if vaultSecretData[secret.Key] == nil {
					newErr := fmt.Errorf(
						"vault path %s is valid but value for %s was not found",
						secret.Path,
						secret.Key,
					)
					regionEnv.Errors = append(regionEnv.Errors, newErr)
					continue
				}
				secret.Value = vaultSecretData[secret.Key].(string)
				secretCache[secret.Path] = vaultSecretData
				secrets = append(secrets, secret)
			}
			regionEnv.Secrets = secrets

			log.Infof("Fetched secrets for %s.", regionEnv.Region)

			select {
			case <-regionEnv.Context.Done():
				log.Info("Received Done")
				return
			case envWithSecretsStream <- regionEnv:
			}
		}
	}()
	return envWithSecretsStream
}

// createEnv creates Kubernetes configmap and secret
func createEnv(kubeServiceConfig KubeObjects, regionEnvStream <-chan *RegionEnv) <-chan *RegionEnv {
	envKubernetesStream := make(chan *RegionEnv)
	// The secrets and config map take their name from the deployment.
	//deployment := kubeServiceConfig.Deployment.(*appsv1.Deployment)
	deploymentName := kubeServiceConfig.parentObject().GetName()
	go func() {
		defer log.Debug("Closing createEnv channel")
		defer close(envKubernetesStream)
		for regionEnv := range regionEnvStream {
			var clientConfig *restclient.Config
			var kubeConfigErr error

			// Check for errors in the stream
			if len(regionEnv.Errors) > 0 {
				envKubernetesStream <- regionEnv
				log.Info("Exiting on previous errors")
				return
			}

			if err := regionEnv.validateSecretPath(deploymentName); err != nil {
				regionEnv.Errors = append(regionEnv.Errors, err)
				envKubernetesStream <- regionEnv
				return
			}

			log.Infof("Modifying region specific Kubernetes structs for %s.", regionEnv.Region)

			if len(kubeServiceConfig.ClusterConfigs.Clusters) < 1 {
				// Follow kubectl convention.
				kubeConfigFile := setFromEnvStr("KUBECONFIG", filepath.Join(os.Getenv("HOME"), ".kube", "config"))
				log.Infof("Using kubeconfig file %s", kubeConfigFile)
				// Region name must be the same as the kubernetes context name.
				clientConfig, kubeConfigErr = buildConfigFromFlags(regionEnv.Region, kubeConfigFile)
				if kubeConfigErr != nil {
					regionEnv.Errors = append(regionEnv.Errors, kubeConfigErr)
					envKubernetesStream <- regionEnv
					log.Info("Exiting on kubeconfig construction errors")
					return
				}
			} else {
				ok := false
				clientConfig, ok = kubeServiceConfig.ClusterConfigs.RegionMap[regionEnv.Region]
				if !ok {
					kubeConfigErr = fmt.Errorf(
						"config for region %s not found in %s",
						regionEnv.Region,
						clusterConfigFile,
					)
					regionEnv.Errors = append(regionEnv.Errors, kubeConfigErr)
					envKubernetesStream <- regionEnv
					log.Info("Exiting on missing config in vault.")
					return
				}
			}

			clientset, clientsetErr := kubernetes.NewForConfig(clientConfig)
			if clientsetErr != nil {
				regionEnv.Errors = append(regionEnv.Errors, clientsetErr)
				envKubernetesStream <- regionEnv
				log.Info("Exiting on clientset creation error.")
				return
			}
			regionEnv.Clientset = clientset
			istioClientset, clientsetErr := istioversioned.NewForConfig(clientConfig)
			if clientsetErr != nil {
				regionEnv.Errors = append(regionEnv.Errors, clientsetErr)
				envKubernetesStream <- regionEnv
				log.Info("Exiting on istio clientset creation error.")
				return
			}
			regionEnv.IstioClientset = istioClientset
			catalogClientset, clientsetErr := catalogclientset.NewForConfig(clientConfig)
			if clientsetErr != nil {
				regionEnv.Errors = append(regionEnv.Errors, clientsetErr)
				envKubernetesStream <- regionEnv
				log.Info("Exiting on catalog clientset creation error.")
				return
			}
			regionEnv.CatalogClientset = catalogClientset
			contourClientset, clientsetErr := contourversioned.NewForConfig(clientConfig)
			if clientsetErr != nil {
				regionEnv.Errors = append(regionEnv.Errors, clientsetErr)
				envKubernetesStream <- regionEnv
				log.Info("Exiting on contour clientset creation error.")
				return
			}
			regionEnv.ContourClientset = contourClientset

			// Retrieve cluster specific porter2k8s settings from "porter2k8s" configmap.
			regionEnv.porter2k8sConfigMap()
			log.Infof("Cluster settings %+v", regionEnv.ClusterSettings)

			// Load regionEnv.Secrets of Kind "porter2k8s" into the region's cluster settings
			for _, secret := range regionEnv.Secrets {
				if secret.Kind == "porter2k8s" {
					regionEnv.ClusterSettings[secret.Name] = secret.Value
				}
			}

			// Inject the logging sidecar if necessary before it gets copied to the regions
			if loggingSidecarInjectionRequired(regionEnv.ClusterSettings) {
				loggingSidecarInjectionSucceeded := regionEnv.addLoggingSidecarContainer(kubeServiceConfig)
				if !loggingSidecarInjectionSucceeded {
					envKubernetesStream <- regionEnv
					log.Info("Exiting on logging sidecar injection error.")
					return
				}
			}

			// Each RegionEnv needs its own copy of the kubernetes objects, since they are modified with region
			// specific settings.
			regionEnv.ConfigMap = kubeServiceConfig.ConfigMap.DeepCopy()
			regionEnv.Deployment = kubeServiceConfig.Deployment.DeepCopy()
			regionEnv.Job = kubeServiceConfig.Job.DeepCopy()
			regionEnv.Gateway = kubeServiceConfig.Gateway.DeepCopy()
			regionEnv.HPAutoscaler = kubeServiceConfig.HPAutoscaler.DeepCopy()
			regionEnv.Ingress = kubeServiceConfig.Ingress.DeepCopy()
			regionEnv.PodDisruptionBudget = kubeServiceConfig.PodDisruptionBudget.DeepCopy()
			regionEnv.Service = kubeServiceConfig.Service.DeepCopy()
			regionEnv.ServiceAccount = kubeServiceConfig.ServiceAccount.DeepCopy()
			regionEnv.VirtualService = kubeServiceConfig.VirtualService.DeepCopy()
			regionEnv.IngressRouteList = kubeServiceConfig.IngressRouteList.DeepCopy()
			regionEnv.NamedSecrets = kubeServiceConfig.NamedSecrets.DeepCopy()

			// More than one service instance or binding are allowed.
			for cloudKey, serviceInstances := range kubeServiceConfig.ServiceInstances {
				for _, serviceInstance := range serviceInstances {
					regionEnv.ServiceInstances[cloudKey] = append(
						regionEnv.ServiceInstances[cloudKey],
						serviceInstance.DeepCopy(),
					)
				}
			}
			for cloudKey, serviceBindings := range kubeServiceConfig.ServiceBindings {
				for _, serviceBinding := range serviceBindings {
					regionEnv.ServiceBindings[cloudKey] = append(
						regionEnv.ServiceBindings[cloudKey],
						serviceBinding.DeepCopy(),
					)
				}
			}

			configMapCreateSucceeded := regionEnv.createConfigMap(deploymentName)
			if !configMapCreateSucceeded {
				envKubernetesStream <- regionEnv
				log.Info("Exiting on configmap creation error.")
				return
			}

			secretCreateSucceeded := regionEnv.createSecret(deploymentName)
			if !secretCreateSucceeded {
				envKubernetesStream <- regionEnv
				log.Info("Exiting on secret creation failure.")
				return
			}

			select {
			case <-regionEnv.Context.Done():
				log.Info("Received Done")
				return
			case envKubernetesStream <- regionEnv:
			}
		}
	}()
	return envKubernetesStream
}

// FlagSet - Set up the flags
func flagSet(name string, cfg *CmdConfig) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ExitOnError)
	flags.StringVar(
		&cfg.ConfigPath,
		"config-path",
		setFromEnvStr("CONFIG_PATH", "/repo"),
		"Configuration root directory. Should include the '.porter' or 'environment' directory. "+
			"Kubernetes object yaml files may be in the directory or in a subdirectory named 'k8s'.",
	)
	flags.StringVar(&cfg.ConfigType, "config-type", setFromEnvStr("CONFIG_TYPE", "porter"), "Configuration type, "+
		"simple or porter.")
	flags.StringVar(&cfg.Environment, "environment", setFromEnvStr("ENVIRONMENT", ""), "Environment of deployment.")
	flags.IntVar(&cfg.MaxConfigMaps, "max-cm", setFromEnvInt("MAX_CM", 5), "Maximum number of configmaps and secret "+
		"objects to keep per app.")
	flags.StringVar(&cfg.Namespace, "namespace", setFromEnvStr("NAMESPACE", "default"), "Kubernetes namespace.")
	flags.StringVar(&cfg.Regions, "regions", setFromEnvStr("REGIONS", ""), "Regions"+
		"of deployment. (Multiple Space delimited regions allowed)")
	flags.StringVar(&cfg.SHA, "sha", setFromEnvStr("sha", ""), "Deployment sha.")
	flags.StringVar(&cfg.VaultAddress, "vault-addr", setFromEnvStr("VAULT_ADDR", "https://vault.loc.adobe.net"),
		"Vault server.")
	flags.StringVar(&cfg.VaultBasePath, "vault-path", setFromEnvStr("VAULT_PATH", "/"), "Path in Vault.")
	flags.StringVar(&cfg.VaultToken, "vault-token", setFromEnvStr("VAULT_TOKEN", ""), "Vault token.")
	flags.StringVar(&cfg.SecretPathWhiteList, "secret-path-whitelist", setFromEnvStr("SECRET_PATH_WHITELIST", ""), ""+
		"Multiple Space delimited secret path whitelist allowed")
	flags.BoolVar(&cfg.Verbose, "v", setFromEnvBool("VERBOSE"), "Verbose log output.")
	flags.IntVar(&cfg.Wait, "wait", setFromEnvInt("WAIT", 180), "Extra time to wait for deployment to complete in "+
		"seconds.")
	flags.StringVar(&cfg.LogMode, "log-mode", setFromEnvStr("LOG_MODE", "inline"), "Pod log streaming mode. "+
		"One of 'inline' (print to porter2k8s log), 'file' (write to filesystem, see log-dir option), "+
		"'none' (disable log streaming)")
	flags.StringVar(&cfg.LogDir, "log-dir", setFromEnvStr("LOG_DIR", "logs"),
		"Directory to write pod logs into. (must already exist)")

	return flags
}

// Parse - Process command line arguments
func parse(args []string, cfg *CmdConfig) error {

	flags := flagSet("<global options> ", cfg)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if cfg.Regions == "" {
		return fmt.Errorf("no regions given")
	}
	if cfg.Environment == "" {
		return fmt.Errorf("no environment given")
	}
	if cfg.SHA == "" {
		return fmt.Errorf("no deployment SHA given")
	}
	if cfg.MaxConfigMaps < 3 {
		return fmt.Errorf("number of configmaps and secret objects to keep must be more than 2")
	}
	if cfg.Verbose {
		log.SetLevel(log.DebugLevel)
		log.Debug("Setting verbose logging")
	}
	switch cfg.LogMode {
	case "inline":
		cfg.PodLogSinker = LogrusSink()
	case "file":
		fileInfo, err := os.Stat(cfg.LogDir)
		if err == nil && !fileInfo.IsDir() {
			err = fmt.Errorf("not a directory")
		}
		if err != nil {
			return fmt.Errorf("invalid log-dir: %s (%w)", cfg.LogDir, err)
		}
		cfg.PodLogSinker = DirectoryLogSink(cfg.LogDir)
	case "none":
		cfg.PodLogSinker = nil
	default:
		return fmt.Errorf("invalid log-mode: %s", cfg.LogMode)
	}

	log.Infof("Regions: %s", cfg.Regions)
	return nil
}

// Usage - emit the usage
func usage(writer io.Writer, cfg *CmdConfig) {
	flags := flagSet("<global options help>", cfg)
	flags.SetOutput(writer)
	flags.PrintDefaults()
}

// findContainer finds the container in deployment/job that matches the deployment name.
func findContainer(kubeObject metav1.Object) *v1.Container {
	name := kubeObject.GetName()
	templateSpec := findPodTemplateSpec(kubeObject)

	// Find container in deployment that matches the deployment name
	for i, container := range templateSpec.Spec.Containers {
		if container.Name == name {
			return &templateSpec.Spec.Containers[i]
		}
	}
	return nil
}

func findPodTemplateSpec(kubeObject interface{}) *v1.PodTemplateSpec {
	switch asserted := kubeObject.(type) {
	case *appsv1.Deployment:
		return &asserted.Spec.Template
	case *batchv1.Job:
		return &asserted.Spec.Template
	default:
		return nil
	}
}

// buildConfigFromFlags creates Kube Config.
// Context is assumed to be the region name.
func buildConfigFromFlags(context, kubeconfigPath string) (*restclient.Config, error) {
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{
			CurrentContext: context,
		}).ClientConfig()
}

// calculateWatchTimeout calculates timeout for watch channel based on livenessProbe for primary pod container
// The idea is for porter2k8s to fail if the containers fail to come up healthy.
func calculateWatchTimeout(deployment *appsv1.Deployment, buffer int32) *int64 {
	applicationContainer := findContainer(deployment)
	if applicationContainer.LivenessProbe == nil {
		// Return default if liveness probe was initialized but all values left at defaults, plus buffer.
		return int64Ref(33 + buffer)
	}
	// Default settings as defined in k8s.io/api/core/v1/types.go
	initialDelay := applicationContainer.LivenessProbe.InitialDelaySeconds // Default is 0.
	timeout := setIfZeroInt32(applicationContainer.LivenessProbe.TimeoutSeconds, 1)
	period := setIfZeroInt32(applicationContainer.LivenessProbe.PeriodSeconds, 10)
	failureThreshold := setIfZeroInt32(applicationContainer.LivenessProbe.FailureThreshold, 3)
	return int64Ref(buffer + initialDelay + failureThreshold*(period+timeout))
}

// setReplicas sets the number of replicas in the pending deployment to the greater number
// between the previous and soon to be updated deployment version.
func setReplicas(pendingDeployment, previousDeployment *appsv1.Deployment) {
	if pendingDeployment.Spec.Replicas == nil || *previousDeployment.Spec.Replicas > *pendingDeployment.Spec.Replicas {
		log.Infof("Setting number of replicas to pre-existing value of %d", *previousDeployment.Spec.Replicas)
		pendingDeployment.Spec.Replicas = previousDeployment.Spec.Replicas
	}
}

// prepareDeployment takes the deployment from the yaml file and applies image sha, labels, and configmap and secret
// references.
func prepareDeployment(deploymentObj metav1.Object, sha string) error {
	// Add sha to image, configMapRef, and secretRef
	// Find container in deployment that matches the deployment name
	applicationContainer := findContainer(deploymentObj)
	if applicationContainer == nil {
		return fmt.Errorf("unable to find application image in deployment spec")
	}

	// Remove docker tag, if it exists.
	// The image in the deployment.yaml should not have a tag.
	shaReplacement := fmt.Sprintf("$1:%s", sha)
	// Replace everything after the colon, if it exists. See tests for examples.
	regex := regexp.MustCompile(`(.*?)(:|\z).*`)
	applicationContainer.Image = regex.ReplaceAllString(applicationContainer.Image, shaReplacement)

	refName := fmt.Sprintf("%s-%s", deploymentObj.GetName(), sha)
	// Stub out references to configmap and secret ref to be filled out for each region later.
	envSourceConfigMap := v1.EnvFromSource{
		ConfigMapRef: &v1.ConfigMapEnvSource{
			v1.LocalObjectReference{
				Name: refName,
			},
			boolRef(false),
		},
	}
	envSourceSecret := v1.EnvFromSource{
		SecretRef: &v1.SecretEnvSource{
			v1.LocalObjectReference{
				Name: refName,
			},
			// Secrets may not be defined.
			boolRef(true),
		},
	}
	applicationContainer.EnvFrom = append(applicationContainer.EnvFrom, envSourceConfigMap, envSourceSecret)

	// Add sha label to deployment and pod.
	appendLabel(deploymentObj, "sha", sha)
	appendLabel(findPodTemplateSpec(deploymentObj), "sha", sha)

	return nil
}
