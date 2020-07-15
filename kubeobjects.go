package porter2k8s

import (
	"fmt"
	"os"
	"reflect"

	vaultapi "github.com/hashicorp/vault/api"
	catalogv1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	contourv1beta1 "github.com/projectcontour/contour/apis/contour/v1beta1"
	log "github.com/sirupsen/logrus"
	istiov1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	autoscaling "k8s.io/api/autoscaling/v2beta2"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	policy "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KubeObjects is the global configuration for the service.
type KubeObjects struct {
	ClusterConfigs      ClusterConfigs
	ConfigMap           *v1.ConfigMap
	Deployment          *appsv1.Deployment
	Job                 *batchv1.Job
	Gateway             *istiov1beta1.Gateway
	HPAutoscaler        *autoscaling.HorizontalPodAutoscaler
	Ingress             *extensionsv1beta1.Ingress
	IngressRouteList    *contourv1beta1.IngressRouteList
	LoggingPod          *v1.Pod // Logging sidecar is defined as a pod in the k8s-template repo
	PodDisruptionBudget *policy.PodDisruptionBudget
	NamedSecrets        *v1.SecretList // Secret objects defined by service teams
	Service             *v1.Service
	ServiceAccount      *v1.ServiceAccount
	ServiceInstances    map[string][]*catalogv1beta1.ServiceInstance // Open Service Broker Service Instances.
	ServiceBindings     map[string][]*catalogv1beta1.ServiceBinding  // Open Service Broker Service Bindings.
	VirtualService      *istiov1beta1.VirtualService
}

// getKubeConfigs fetches Kube Configs from vault.
func (kubeObjects *KubeObjects) getKubeConfigs(cfg *CmdConfig) error {
	clusterListFileName := fmt.Sprintf("%s/%s", cfg.ConfigPath, clusterConfigFile)
	if _, err := os.Stat(clusterListFileName); err == nil {
		log.Infof("Retrieving kubeconfig from Vault using %s", clusterListFileName)
		client, err := newVaultClient(cfg.VaultAddress, cfg.VaultToken)
		if err != nil {
			return fmt.Errorf("vault client initialization error %s\n%s", cfg.ConfigType, err)
		}
		err = kubeObjects.KubeConfigFromVault(client, clusterListFileName)
		if err != nil {
			return fmt.Errorf("unable to pull cluster configuration from Vault %s", err)
		}
	}
	return nil
}

func (kubeObjects KubeObjects) parentObject() metav1.Object {
	if kubeObjects.Deployment != nil {
		return kubeObjects.Deployment
	}
	return kubeObjects.Job
}

// KubeConfigFromVault reads a cluster config file, pulls from Vault and instantiates k8s client cluster configurations.
func (kubeObjects *KubeObjects) KubeConfigFromVault(vaultClient *vaultapi.Client, fileName string) error {
	clusters, err := readReferences(fileName)
	if err != nil {
		return err
	}
	err = clusters.fetchConfigs(vaultClient)
	if err != nil {
		return err
	}
	kubeObjects.ClusterConfigs = clusters
	return nil
}

// Read objects from yaml files and populate kubeObjects.
func (kubeObjects *KubeObjects) processObjects(cfg *CmdConfig, possibleFiles []string) error {
	// Determine if kubernetes yaml files are in the config directory or the k8s subdirectory
	objectDirectory := fmt.Sprintf("%s/k8s", cfg.ConfigPath)
	if _, err := os.Stat(objectDirectory); os.IsNotExist(err) {
		objectDirectory = cfg.ConfigPath
	}
	log.Infof("Using %s as directory for Kubernetes yaml objects.", objectDirectory)

	for _, fileType := range possibleFiles {
		var foundFileName string
		fileNameEnv := fmt.Sprintf("%s/%s-%s.yaml", objectDirectory, fileType, cfg.Environment)
		fileNameGeneral := fmt.Sprintf("%s/%s.yaml", objectDirectory, fileType)
		for _, fileName := range []string{fileNameEnv, fileNameGeneral} {
			if _, err := os.Stat(fileName); os.IsNotExist(err) {
				log.Infof("%s not found.", fileName)
			} else {
				log.Infof("Using %s for %s.", fileName, fileType)
				foundFileName = fileName
				break
			}
		}
		// If no file name was found, try the next possible name.
		if foundFileName == "" {
			continue
		}
		objects, readErr := multiDecoder(foundFileName)
		if readErr != nil {
			return readErr
		}
		for _, decoded := range objects {
			object := decoded.Object
			switch asserted := object.(type) {
			case *v1.Service:
				kubeObjects.Service = asserted
			case *v1.ServiceAccount:
				kubeObjects.ServiceAccount = asserted
			case *appsv1.Deployment:
				kubeObjects.Deployment = asserted
			case *batchv1.Job:
				kubeObjects.Job = asserted
			case *extensionsv1beta1.Ingress:
				kubeObjects.Ingress = asserted
			case *contourv1beta1.IngressRoute:
				kubeObjects.IngressRouteList.Items = append(kubeObjects.IngressRouteList.Items, *asserted)
			case *istiov1beta1.Gateway:
				kubeObjects.Gateway = asserted
			case *istiov1beta1.VirtualService:
				kubeObjects.VirtualService = asserted
			case *autoscaling.HorizontalPodAutoscaler:
				kubeObjects.HPAutoscaler = asserted
			case *v1.ConfigMap:
				kubeObjects.ConfigMap = asserted
			case *v1.Pod:
				kubeObjects.LoggingPod = asserted
			case *policy.PodDisruptionBudget:
				kubeObjects.PodDisruptionBudget = asserted
			case *catalogv1beta1.ServiceInstance:
				if fileType == "servicecatalog-aws" {
					kubeObjects.ServiceInstances["aws"] = append(kubeObjects.ServiceInstances["aws"], asserted)
				} else if fileType == "servicecatalog-azure" {
					kubeObjects.ServiceInstances["azure"] = append(kubeObjects.ServiceInstances["azure"], asserted)
				} else {
					log.Warnf(
						"Excluding Service Instance found in %s, an AWS or Azure specific servicecatalog file",
						foundFileName,
					)
				}
			case *catalogv1beta1.ServiceBinding:
				if fileType == "servicecatalog-aws" {
					kubeObjects.ServiceBindings["aws"] = append(kubeObjects.ServiceBindings["aws"], asserted)
				} else if fileType == "servicecatalog-azure" {
					kubeObjects.ServiceBindings["azure"] = append(kubeObjects.ServiceBindings["azure"], asserted)
				} else {
					log.Warnf(
						"Excluding Service Binding found in %s, an AWS or Azure specific servicecatalog file",
						foundFileName,
					)
				}
			case *v1.Secret:
				kubeObjects.NamedSecrets.Items = append(kubeObjects.NamedSecrets.Items, *asserted)
			default:
				log.Warnf(
					"Object type %s is unsupported, please upgrade to current version to change existing resource",
					decoded.GVK,
				)
			}
		}
	}

	return nil
}

// validate ensure that all kubernetes resources contain the service name.
// This prevents different microservices from colliding.
// It also adds the name to the tags of each resource.
func (kubeObjects *KubeObjects) validateAndTag() error {
	if kubeObjects.Deployment == nil && kubeObjects.Job == nil {
		log.Infof("Deployment %+v", kubeObjects.Deployment)
		return fmt.Errorf("Deployment or Job configuration is required")
	} else if kubeObjects.Deployment != nil && kubeObjects.Job != nil {
		return fmt.Errorf("Only Deployment OR Job configuration is allowed")
	}

	// Additional Deployment checks are handled in prepareDeployment.
	// If the deployment lacks a service object, set the service name
	// to the deployment name, otherwise use the service name
	var serviceName string
	deploymentName := kubeObjects.parentObject().GetName()
	if kubeObjects.Service != nil {
		serviceName = kubeObjects.Service.GetName()
	} else {
		serviceName = deploymentName
	}
	fields := reflect.ValueOf(*kubeObjects)
	for i := 0; i < fields.NumField(); i++ {
		object := fields.Field(i).Interface()
		err := validateObject(object, serviceName, deploymentName)
		if err != nil {
			return err
		}
	}
	return nil
}
