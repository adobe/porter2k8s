package porter2k8s

import (
	"fmt"
	"regexp"
	"sync"

	log "github.com/sirupsen/logrus"
	catalogv1beta1 "github.com/kubernetes-sigs/service-catalog/pkg/apis/servicecatalog/v1beta1"
	contourv1beta1 "github.com/projectcontour/contour/apis/contour/v1beta1"
	istiov1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	autoscaling "k8s.io/api/autoscaling/v2beta2"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	policy "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// runUpdate Creates/Updates objects across regions.
func runUpdate(regionEnvs []*RegionEnv, updateFunc UpdateFn) error {

	// See if all regions made it here.
	log.Debugf("Number of regions: %d", len(regionEnvs))

	// Done channel boiler plate.
	done := make(chan interface{})
	defer close(done)
	// WaitGroup for all of the updates.

	updater := func() <-chan *RegionEnv {
		var wg sync.WaitGroup
		wg.Add(len(regionEnvs))
		objectStream := make(chan *RegionEnv)
		for _, regionEnv := range regionEnvs {
			go updateFunc(done, &wg, regionEnv, objectStream)
		}
		// Close the channel once all regionEnvs have been passed.
		// This needs to be inside the update function, because channels should be closed by the sender.
		go func() {
			wg.Wait()
			close(objectStream)
		}()
		return objectStream
	}

	updates := updater()
	for update := range updates {
		if len(update.Errors) > 0 {
			return fmt.Errorf("Update of %s failed\n%s", update.Region, update.Errors)
		}
	}

	return nil
}

// updateConfigMapRegion updates a ConfigMap for a region
func updateConfigMapRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	configMapStream chan *RegionEnv,
) {
	defer wg.Done()
	// ConfigMap is not required.
	if regionEnv.ConfigMap == nil {
		configMapStream <- regionEnv
		return
	}

	var updateErr, getErr error
	var previousConfigMap, configMapUpdate *v1.ConfigMap

	deploymentName := regionEnv.Deployment.ObjectMeta.Name
	configMapInterface := regionEnv.Clientset.CoreV1().ConfigMaps(regionEnv.Cfg.Namespace)

	// Override metadata objects with deployment-specific values
	regionEnv.ConfigMap.ObjectMeta.Labels["app"] = deploymentName
	name := regionEnv.ConfigMap.ObjectMeta.Name

	previousConfigMap, getErr = configMapInterface.Get(name, metav1.GetOptions{})

	if getErr == nil {
		if needsUpdate(previousConfigMap, regionEnv.ConfigMap) {
			configMapUpdate, updateErr = configMapInterface.Update(regionEnv.ConfigMap)
		}
	} else if errors.IsNotFound(getErr) {
		configMapUpdate, updateErr = configMapInterface.Create(regionEnv.ConfigMap)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get ConfigMap\n%s", getErr))
		configMapStream <- regionEnv
		return
	}

	if updateErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to create/update ConfigMap\n%s", updateErr))
		configMapStream <- regionEnv
		return
	}

	if configMapUpdate != nil {
		log.Infof("Update: %+v", configMapUpdate)
	}
	select {
	case <-done:
		log.Info("Received Done")
		return
	case configMapStream <- regionEnv:
	}
}

// updateNamedSecretsRegion goes here
func updateNamedSecretsRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	namedSecretsStream chan *RegionEnv,
) {
	defer wg.Done()
	// Create secret objects only if they are defined
	if regionEnv.NamedSecrets == nil {
		namedSecretsStream <- regionEnv
		return
	}

	// Replace template placeholders with cluster settings data
	regionEnv.replaceNamedSecretData()

	for _, namedSecret := range regionEnv.NamedSecrets.Items {
		var updateErr, getErr error
		var previousNamedSecret, namedSecretUpdate *v1.Secret
		name := namedSecret.ObjectMeta.Name
		// Contour clientset is region specific.
		namedSecretInterface := regionEnv.Clientset.CoreV1().Secrets(regionEnv.Cfg.Namespace)

		// Check if a named secret already exists.
		previousNamedSecret, getErr = namedSecretInterface.Get(name, metav1.GetOptions{})

		// Update the Secret object if required.
		if getErr == nil {
			if needsUpdate(previousNamedSecret, namedSecret) {
				namedSecret.ObjectMeta.ResourceVersion = previousNamedSecret.ObjectMeta.ResourceVersion
				namedSecretUpdate, updateErr = namedSecretInterface.Update(&namedSecret)
			}
		} else if errors.IsNotFound(getErr) {
			namedSecretUpdate, updateErr = namedSecretInterface.Create(&namedSecret)
		} else {
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get Secret: %s", getErr))
			namedSecretsStream <- regionEnv
			return
		}
		if updateErr != nil {
			regionEnv.Errors = append(
				regionEnv.Errors,
				fmt.Errorf("Unable to create/update Secret: %s", updateErr),
			)
			namedSecretsStream <- regionEnv
			return
		}

		if namedSecretUpdate != nil {
			log.Infof("Update: %+v", namedSecretUpdate)
		}

		select {
		case <-done:
			log.Info("Received Done")
			return
		case namedSecretsStream <- regionEnv:
		}
	}
}

// updateServiceRegion Creates/updates a service.
// Only ClusterIP defined services are supported.
func updateServiceRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	serviceStream chan *RegionEnv,
) {
	defer wg.Done()
	// Make this optional to support worker containers with no Service object
	if regionEnv.Service == nil {
		serviceStream <- regionEnv
		return
	}

	var updateErr, getErr error
	var previousService, serviceUpdate *v1.Service

	name := regionEnv.Service.ObjectMeta.Name
	// Client set is region specific.
	serviceInterface := regionEnv.Clientset.CoreV1().Services(regionEnv.Cfg.Namespace)

	// Check if service already exists.
	previousService, getErr = serviceInterface.Get(name, metav1.GetOptions{})

	// Replace domain name with regional value.
	// This affects all objects with domain names in them, not just services.
	// Since the service is mandatory, we do it here rather than in istio or ingress.
	regionEnv.updateDomainName()

	// Update service if required.
	if getErr == nil {
		if needsUpdate(previousService, regionEnv.Service) {
			// Cluster IP and Resource Version are required to be passed back unaltered.
			regionEnv.Service.ObjectMeta.ResourceVersion = previousService.ObjectMeta.ResourceVersion
			regionEnv.Service.Spec.ClusterIP = previousService.Spec.ClusterIP
			serviceUpdate, updateErr = serviceInterface.Update(regionEnv.Service)
		}
	} else if errors.IsNotFound(getErr) {
		serviceUpdate, updateErr = serviceInterface.Create(regionEnv.Service)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get service\n%s", getErr))
		serviceStream <- regionEnv
		return
	}
	if updateErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to create/update service\n%s", updateErr))
		serviceStream <- regionEnv
		return
	}

	if serviceUpdate != nil {
		log.Infof("Update: %+v", serviceUpdate)
	}

	select {
	case <-done:
		log.Info("Received Done")
		return
	case serviceStream <- regionEnv:
	}
}

// updateServiceAccountRegion creates/updates a service account.
func updateServiceAccountRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	serviceAccountStream chan *RegionEnv,
) {
	defer wg.Done()
	// This optional
	if regionEnv.ServiceAccount == nil {
		serviceAccountStream <- regionEnv
		return
	}

	var updateErr, getErr error
	var previousServiceAccount, serviceAccountUpdate *v1.ServiceAccount

	name := regionEnv.ServiceAccount.ObjectMeta.Name
	// Client set is region specific.
	serviceAccountInterface := regionEnv.Clientset.CoreV1().ServiceAccounts(regionEnv.Cfg.Namespace)

	// Check if service already exists.
	previousServiceAccount, getErr = serviceAccountInterface.Get(name, metav1.GetOptions{})

	// Update service if required.
	if getErr == nil {
		// Secrets are added automatically after instantiation.
		regionEnv.ServiceAccount.Secrets = addReferences(
			regionEnv.ServiceAccount.Secrets,
			previousServiceAccount.Secrets,
		)
		if needsUpdate(previousServiceAccount, regionEnv.ServiceAccount) {
			regionEnv.ServiceAccount.ObjectMeta.ResourceVersion = previousServiceAccount.ObjectMeta.ResourceVersion
			serviceAccountUpdate, updateErr = serviceAccountInterface.Update(regionEnv.ServiceAccount)
		}
	} else if errors.IsNotFound(getErr) {
		serviceAccountUpdate, updateErr = serviceAccountInterface.Create(regionEnv.ServiceAccount)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get service account\n%s", getErr))
		serviceAccountStream <- regionEnv
		return
	}
	if updateErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to create/update service acct\n%s", updateErr))
		serviceAccountStream <- regionEnv
		return
	}

	if serviceAccountUpdate != nil {
		log.Infof("Update: %+v", serviceAccountUpdate)
	}

	select {
	case <-done:
		log.Info("Received Done")
		return
	case serviceAccountStream <- regionEnv:
	}
}

// updateIngressRegion creates/updates an ingress.
func updateIngressRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	ingressStream chan *RegionEnv,
) {
	defer wg.Done()
	// Do not create ingress if the cluster is running istio.
	if regionEnv.Ingress == nil || regionEnv.ClusterSettings["ISTIO"] == "true" {
		ingressStream <- regionEnv
		return
	}

	var updateErr, getErr error
	var ingressUpdate *extensionsv1beta1.Ingress

	name := regionEnv.Ingress.ObjectMeta.Name
	// Client set is region specific.
	ingressInterface := regionEnv.Clientset.ExtensionsV1beta1().Ingresses(regionEnv.Cfg.Namespace)

	// Check if ingress already exists.
	previousIngress, getErr := ingressInterface.Get(name, metav1.GetOptions{})

	// Update ingress if required.
	if getErr == nil {
		if needsUpdate(previousIngress, regionEnv.Ingress) {
			ingressUpdate, updateErr = ingressInterface.Update(regionEnv.Ingress)
		}
	} else if errors.IsNotFound(getErr) {
		ingressUpdate, updateErr = ingressInterface.Create(regionEnv.Ingress)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get ingress\n%s", getErr))
		ingressStream <- regionEnv
		return
	}
	if updateErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to create/update ingress\n%s", updateErr))
		ingressStream <- regionEnv
		return
	}

	if ingressUpdate != nil {
		log.Infof("Update: %+v", ingressUpdate)
	}

	select {
	case <-done:
		log.Info("Received Done")
		return
	case ingressStream <- regionEnv:
	}
}

// updateIngressRouteRegion creates/updates a Contour IngressRouteList
func updateIngressRouteRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	ingressRouteStream chan *RegionEnv,
) {
	defer wg.Done()
	// Create IngressRoute only if the cluster is NOT running istio (e.g., ethos-k8s).
	if regionEnv.IngressRouteList == nil || regionEnv.ClusterSettings["ISTIO"] == "true" {
		ingressRouteStream <- regionEnv
		return
	}

	// Iterate over the IngressRouteList created during updateDomainName()
	// and create/update them as needed
	for _, ingressroute := range regionEnv.IngressRouteList.Items {
		var updateErr, getErr error
		var ingressRouteUpdate *contourv1beta1.IngressRoute

		name := ingressroute.ObjectMeta.Name
		// Contour clientset is region specific.
		ingressRouteInterface := regionEnv.ContourClientset.ContourV1beta1().IngressRoutes(regionEnv.Cfg.Namespace)

		// Check if an IngressRoute already exists.
		previousIngressRoute, getErr := ingressRouteInterface.Get(name, metav1.GetOptions{})

		// Update the IngressRoute if required.
		if getErr == nil {
			if needsUpdate(previousIngressRoute, ingressroute) {
				ingressroute.ObjectMeta.ResourceVersion = previousIngressRoute.ObjectMeta.ResourceVersion
				ingressRouteUpdate, updateErr = ingressRouteInterface.Update(&ingressroute)
			}
		} else if errors.IsNotFound(getErr) {
			ingressRouteUpdate, updateErr = ingressRouteInterface.Create(&ingressroute)
		} else {
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get IngressRoute: %s", getErr))
			ingressRouteStream <- regionEnv
			return
		}
		if updateErr != nil {
			regionEnv.Errors = append(
				regionEnv.Errors,
				fmt.Errorf("Unable to create/update IngressRoute: %s", updateErr),
			)
			ingressRouteStream <- regionEnv
			return
		}

		if ingressRouteUpdate != nil {
			log.Infof("Update: %+v", ingressRouteUpdate)
		}

		select {
		case <-done:
			log.Info("Received Done")
			return
		case ingressRouteStream <- regionEnv:
		}
	}
}

// updateGatewayRegion creates/updates an Istio gateway.
func updateGatewayRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	gatewayStream chan *RegionEnv,
) {
	defer wg.Done()
	// Do not create gateway if the cluster is not running istio or the service does not have a gateway.
	if regionEnv.ClusterSettings["ISTIO"] != "true" || regionEnv.Gateway == nil {
		log.Info("Not applying Istio Gateway.")
		gatewayStream <- regionEnv
		return
	}
	var updateErr, getErr error
	var gatewayUpdate *istiov1beta1.Gateway

	name := regionEnv.Gateway.ObjectMeta.Name
	// Client set is region specific.
	gatewayInterface := regionEnv.IstioClientset.NetworkingV1beta1().Gateways(regionEnv.Cfg.Namespace)

	// Check if gateway already exists.
	previousGateway, getErr := gatewayInterface.Get(name, metav1.GetOptions{})

	// Update gateway if required.
	if getErr == nil {
		if needsUpdate(previousGateway, regionEnv.Gateway) {
			// Resource Version is required to be passed back unaltered.
			regionEnv.Gateway.ObjectMeta.ResourceVersion = previousGateway.ObjectMeta.ResourceVersion
			gatewayUpdate, updateErr = gatewayInterface.Update(regionEnv.Gateway)
		}
	} else if errors.IsNotFound(getErr) {
		gatewayUpdate, updateErr = gatewayInterface.Create(regionEnv.Gateway)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get gateway\n%s", getErr))
		gatewayStream <- regionEnv
		return
	}

	if updateErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to create/update gateway\n%s", updateErr))
		gatewayStream <- regionEnv
		return
	}

	if gatewayUpdate != nil {
		log.Infof("Update: %+v", gatewayUpdate)
	}

	select {
	case <-done:
		log.Info("Received Done")
		return
	case gatewayStream <- regionEnv:
	}
}

// updateVirtualServiceRegion creates/updates an Istio virtualservice.
func updateVirtualServiceRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	virtualServiceStream chan *RegionEnv,
) {
	defer wg.Done()
	// Do not create virtualservice if the cluster is not running istio.
	if regionEnv.ClusterSettings["ISTIO"] != "true" || regionEnv.VirtualService == nil {
		log.Info("Not applying Istio Virtual Service.")
		virtualServiceStream <- regionEnv
		return
	}
	var updateErr, getErr error
	var virtualserviceUpdate *istiov1beta1.VirtualService

	name := regionEnv.VirtualService.ObjectMeta.Name
	// Client set is region specific.
	virtualserviceInterface := regionEnv.IstioClientset.NetworkingV1beta1().VirtualServices(regionEnv.Cfg.Namespace)

	// Check if virtualservice already exists.
	previousVirtualService, getErr := virtualserviceInterface.Get(name, metav1.GetOptions{})

	// Replace Istio gateway value with the namespace's value.
	regionEnv.updateGateway()

	// Update virtualservice if required.
	if getErr == nil {
		if needsUpdate(previousVirtualService, regionEnv.VirtualService) {
			// Resource Version is required to be passed back unaltered.
			regionEnv.VirtualService.ObjectMeta.ResourceVersion = previousVirtualService.ObjectMeta.ResourceVersion
			virtualserviceUpdate, updateErr = virtualserviceInterface.Update(regionEnv.VirtualService)
		}
	} else if errors.IsNotFound(getErr) {
		virtualserviceUpdate, updateErr = virtualserviceInterface.Create(regionEnv.VirtualService)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get virtualservice\n%s", getErr))
		virtualServiceStream <- regionEnv
		return
	}
	if updateErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to create/update virtualservice\n%s", updateErr))
		virtualServiceStream <- regionEnv
		return
	}

	if virtualserviceUpdate != nil {
		log.Infof("Update: %+v", virtualserviceUpdate)
	}
	select {
	case <-done:
		log.Info("Received Done")
		return
	case virtualServiceStream <- regionEnv:
	}
}

// updateDeploymentRegion creates/updates and monitors a deployment.
func updateDeploymentRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	deploymentStream chan *RegionEnv,
) {
	defer wg.Done()
	// Deployment is not required when deploying a Job
	if regionEnv.Deployment == nil {
		deploymentStream <- regionEnv
		return
	}

	var updateErr, getErr, watchErr error
	var previousDeployment, deploymentUpdate *appsv1.Deployment
	var deploymentSuccessful bool
	name := regionEnv.Deployment.ObjectMeta.Name

	// Client set is region specific.
	deploymentInterface := regionEnv.Clientset.AppsV1().Deployments(regionEnv.Cfg.Namespace)

	// Check if deployment already exists.
	previousDeployment, getErr = deploymentInterface.Get(name, metav1.GetOptions{})

	// Replace docker registry with regional value.
	regionEnv.updateRegistry(regionEnv.Deployment)

	// Replace tokens in annotations.
	regionEnv.replaceDeploymentAnnotations(regionEnv.Deployment)

	// Inject service binding secrets.
	regionEnv.injectSecretKeyRefs(regionEnv.Deployment)

	// Fail deployment if any of the RegionEnv methods produced errors.
	if len(regionEnv.Errors) > 0 {
		deploymentStream <- regionEnv
		return
	}

	log.Debugf("Update: %+v", regionEnv.Deployment)
	// Update deployment if it exists.
	if getErr == nil {
		// Compare number of replicas with current size to ensure we do not decrease the number.
		setReplicas(regionEnv.Deployment, previousDeployment)
		deploymentUpdate, updateErr = deploymentInterface.Update(regionEnv.Deployment)
	} else if errors.IsNotFound(getErr) {
		deploymentUpdate, updateErr = deploymentInterface.Create(regionEnv.Deployment)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to get deployment\n%s", getErr))
		deploymentStream <- regionEnv
		return
	}
	if updateErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("Unable to create/update deployment\n%s", updateErr))
		deploymentStream <- regionEnv
		return
	}
	// Watch deployment update.
	// Rollback not required since old containers will not be removed on a failed deployment.
	deploymentSuccessful, watchErr = regionEnv.watchDeployment(deploymentUpdate)
	if watchErr != nil {
		regionEnv.Errors = append(regionEnv.Errors,
			fmt.Errorf("failure while watching deployment %s,\n%s", regionEnv.Region, watchErr),
		)
	} else if !deploymentSuccessful {
		regionEnv.Errors = append(regionEnv.Errors,
			fmt.Errorf("unknown failure while watching deployment %s", regionEnv.Region),
		)
	}

	if deploymentSuccessful {
		if cleanupErr := regionEnv.postDeployCleanup(previousDeployment); cleanupErr != nil {
			regionEnv.Errors = append(regionEnv.Errors, cleanupErr)
		}
	}

	select {
	case <-done:
		log.Info("Received Done")
		return
	case deploymentStream <- regionEnv:
	}
}

// Runs, tracks and cleans up a batch job.
func updateJobRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	jobStream chan *RegionEnv,
) {
	defer wg.Done()
	// Job is not required when deploying a Deployment
	if regionEnv.Job == nil {
		jobStream <- regionEnv
		return
	}

	var createErr, deleteErr, watchErr error
	var newJob *batchv1.Job
	name := regionEnv.Job.Name

	// Client set is region specific.
	jobInterface := regionEnv.Clientset.BatchV1().Jobs(regionEnv.Cfg.Namespace)

	// Delete if job already exists.
	if previousJob, getErr := jobInterface.Get(name, metav1.GetOptions{}); getErr == nil {
		log.Infof("Found existing job %s in %s - trying to delete", name, regionEnv.Region)
		if deleteErr = regionEnv.deleteJob(previousJob, true); deleteErr != nil {
			regionEnv.Errors = append(regionEnv.Errors,
				fmt.Errorf("Unable to delete preexisting job %s in %s: %w", name, regionEnv.Region, deleteErr))
			jobStream <- regionEnv
			return
		}
		log.Infof("Job %s deleted in %s", name, regionEnv.Region)
	}

	// Replace docker registry with regional value.
	regionEnv.updateRegistry(regionEnv.Job)

	// Replace tokens in annotations.
	regionEnv.replaceDeploymentAnnotations(regionEnv.Job)

	// Inject service binding secrets.
	regionEnv.injectSecretKeyRefs(regionEnv.Job)

	// Fail deployment if any of the RegionEnv methods produced errors.
	if len(regionEnv.Errors) > 0 {
		jobStream <- regionEnv
		return
	}

	log.Debugf("Run: %+v", regionEnv.Job)
	// Create the job
	newJob, createErr = jobInterface.Create(regionEnv.Job)
	if createErr != nil {
		regionEnv.Errors = append(regionEnv.Errors,
			fmt.Errorf("Unable to create job %s in %s: %w", name, regionEnv.Region, createErr))
		jobStream <- regionEnv
		return
	}

	// Watch job execution.
	newJob, watchErr = regionEnv.watchJob(newJob)
	if watchErr != nil {
		regionEnv.Errors = append(regionEnv.Errors,
			fmt.Errorf("failure while watching job %s in %s: %w", name, regionEnv.Region, watchErr),
		)
	} else {
		jobCondition := &newJob.Status.Conditions[len(newJob.Status.Conditions)-1]
		switch jobCondition.Type {
		case batchv1.JobComplete:
			log.Infof("job %s in %s completed at %v", name, regionEnv.Region, newJob.Status.CompletionTime)

		case batchv1.JobFailed:
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf(
				"job %s failed in %s: %s - %s", name, regionEnv.Region, jobCondition.Reason, jobCondition.Message),
			)
		default:
			log.Warnf("job %s unknown status: %+v\n", regionEnv.Region, newJob.Status.Conditions)
		}
	}

	// Cleanup Job, configmaps, secrets
	protectedShas := map[string]bool{}
	deleteErr = regionEnv.deleteJob(newJob, false)
	if deleteErr != nil {
		regionEnv.Errors = append(regionEnv.Errors,
			fmt.Errorf("Unable to clean up job %s in %s: %w", name, regionEnv.Region, deleteErr))
	}
	if !regionEnv.deleteOldConfigMaps(name, protectedShas) {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("configmap deletion error"))
	}
	if !regionEnv.deleteOldSecrets(name, protectedShas) {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("secret deletion failure"))
	}

	select {
	case <-done:
		log.Info("Received Done")
		return
	case jobStream <- regionEnv:
	}
}

// updateHPAutoscalerRegion Creates/updates a horizontal pod autoscaler.
func updateHPAutoscalerRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	hpautoscalerStream chan *RegionEnv,
) {
	defer wg.Done()
	// HPA is not required.
	if regionEnv.HPAutoscaler == nil {
		hpautoscalerStream <- regionEnv
		return
	}

	var updateErr, getErr error
	var previousHPAutoscaler, hpautoscalerUpdate *autoscaling.HorizontalPodAutoscaler

	name := regionEnv.HPAutoscaler.ObjectMeta.Name

	// Increase minimum replica number if required.
	regionEnv.updateHPAMinimum()

	// Client set is region specific.
	hpautoscalerInterface := regionEnv.Clientset.AutoscalingV2beta2().HorizontalPodAutoscalers(regionEnv.Cfg.Namespace)

	// Check if hpautoscaler already exists.
	previousHPAutoscaler, getErr = hpautoscalerInterface.Get(name, metav1.GetOptions{})

	// Update hpautoscaler if required.
	if getErr == nil {
		if needsUpdate(previousHPAutoscaler, regionEnv.HPAutoscaler) {
			// Cluster IP and Resource Version are required to be passed back unaltered.
			regionEnv.HPAutoscaler.ObjectMeta.ResourceVersion = previousHPAutoscaler.ObjectMeta.ResourceVersion
			hpautoscalerUpdate, updateErr = hpautoscalerInterface.Update(regionEnv.HPAutoscaler)
		}
	} else if errors.IsNotFound(getErr) {
		hpautoscalerUpdate, updateErr = hpautoscalerInterface.Create(regionEnv.HPAutoscaler)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("unable to get horizontal pod autoscaler%s", getErr))
		hpautoscalerStream <- regionEnv
		return
	}
	if updateErr != nil {
		regionEnv.Errors = append(regionEnv.Errors,
			fmt.Errorf("unable to create/update hpautoscaler\n%s\n Resource versions: previous %s, update %s",
				updateErr,
				previousHPAutoscaler.ObjectMeta.ResourceVersion,
				regionEnv.HPAutoscaler.ObjectMeta.ResourceVersion,
			),
		)
		hpautoscalerStream <- regionEnv
		return
	}

	if hpautoscalerUpdate != nil {
		log.Infof("Update: %+v", hpautoscalerUpdate)
	}
	select {
	case <-done:
		log.Info("Received Done")
		return
	case hpautoscalerStream <- regionEnv:
	}
}

// updatePodDisruptionBudgetRegion Creates/updates a pod disruption budget.
func updatePodDisruptionBudgetRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	podDisruptionBudgetStream chan *RegionEnv,
) {
	defer wg.Done()
	// PodDisruptionBudget is not required.
	if regionEnv.PodDisruptionBudget == nil {
		podDisruptionBudgetStream <- regionEnv
		return
	}

	var updateErr, deleteErr, getErr error
	var previousPodDisruptionBudget, podDisruptionBudgetUpdate *policy.PodDisruptionBudget

	name := regionEnv.PodDisruptionBudget.ObjectMeta.Name

	// Client set is region specific.
	podDisruptionBudgetInterface := regionEnv.Clientset.PolicyV1beta1().PodDisruptionBudgets(regionEnv.Cfg.Namespace)

	// Check if podDisruptionBudget already exists.
	previousPodDisruptionBudget, getErr = podDisruptionBudgetInterface.Get(name, metav1.GetOptions{})

	if getErr == nil {
		if needsUpdate(previousPodDisruptionBudget, regionEnv.PodDisruptionBudget) {
			// Pod Disruption Budgets cannot be updated until K8s 1.15.
			propagationPolicy := metav1.DeletePropagationForeground
			deleteOptions := metav1.DeleteOptions{PropagationPolicy: &propagationPolicy}
			deleteErr = podDisruptionBudgetInterface.Delete(name, &deleteOptions)
			podDisruptionBudgetUpdate, updateErr = podDisruptionBudgetInterface.Create(regionEnv.PodDisruptionBudget)
		}
	} else if errors.IsNotFound(getErr) {
		podDisruptionBudgetUpdate, updateErr = podDisruptionBudgetInterface.Create(regionEnv.PodDisruptionBudget)
	} else {
		regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("unable to get pod disruption budget%s", getErr))
		podDisruptionBudgetStream <- regionEnv
		return
	}
	if updateErr != nil || deleteErr != nil {
		regionEnv.Errors = append(
			regionEnv.Errors,
			fmt.Errorf("unable to create/update podDisruptionBudget\n%s%s", updateErr, deleteErr),
		)
		podDisruptionBudgetStream <- regionEnv
		return
	}

	if podDisruptionBudgetUpdate != nil {
		log.Infof("Update: %+v", podDisruptionBudgetUpdate)
	}
	select {
	case <-done:
		log.Info("Received Done")
		return
	case podDisruptionBudgetStream <- regionEnv:
	}
}

// updateServiceInstanceRegion Creates/updates a service instance.
func updateServiceInstanceRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	serviceInstanceStream chan *RegionEnv,
) {
	defer wg.Done()

	// Cluster configmap must have the "cloud" key for service instances to be created.
	cloud, ok := regionEnv.ClusterSettings["CLOUD"]
	if !ok {
		log.Errorf("Cannot create or update Service Instances in %s, as the porter2k8s config map does not have a "+
			"'cloud' key", regionEnv.Region)
		serviceInstanceStream <- regionEnv
		return
	}

	// Catalog Service Instances are not required.
	if len(regionEnv.ServiceInstances[cloud]) == 0 {
		serviceInstanceStream <- regionEnv
		return
	}

	var updateErr, getErr, watchErr error
	var serviceInstanceSuccessful bool
	var previousServiceInstance, serviceInstanceUpdate *catalogv1beta1.ServiceInstance
	// Clientset is region specific.
	serviceInstanceInterface := regionEnv.CatalogClientset.ServicecatalogV1beta1().ServiceInstances(
		regionEnv.Cfg.Namespace)

	// Update any tokens in the spec. For example VNET or ACCOUNT.
	regionEnv.replaceServiceInstanceParameters(cloud)

	for _, serviceInstance := range regionEnv.ServiceInstances[cloud] {
		name := serviceInstance.ObjectMeta.Name
		if location, ok := regionEnv.ClusterSettings["CLOUD_LOCATION"]; ok {
			locationRegex := regexp.MustCompile(`"location":"\w+"`)
			locationUpdate := []byte(fmt.Sprintf("\"location\":\"%s\"", location))
			serviceInstance.Spec.Parameters.Raw = locationRegex.ReplaceAllLiteral(
				serviceInstance.Spec.Parameters.Raw,
				locationUpdate,
			)
			// Append location to resource group.
			if cloud == "azure" {
				resourceGroupRegex := regexp.MustCompile(`"resourceGroup":"(\w+)"`)
				resourceGroupUpdate := []byte(fmt.Sprintf("\"resourceGroup\":\"$1-%s\"", location))
				serviceInstance.Spec.Parameters.Raw = resourceGroupRegex.ReplaceAll(
					serviceInstance.Spec.Parameters.Raw, resourceGroupUpdate)
			}
		}

		// Check if serviceInstance already exists.
		previousServiceInstance, getErr = serviceInstanceInterface.Get(name, metav1.GetOptions{})

		if getErr == nil {
			if needsUpdate(previousServiceInstance, serviceInstance) {
				// Resource Version is required to be passed back unaltered.
				serviceInstance.ObjectMeta.ResourceVersion = previousServiceInstance.ObjectMeta.ResourceVersion
				serviceInstance.Spec.ExternalID = previousServiceInstance.Spec.ExternalID
				serviceInstanceUpdate, updateErr = serviceInstanceInterface.Update(serviceInstance)
			}
		} else if errors.IsNotFound(getErr) {
			serviceInstanceUpdate, updateErr = serviceInstanceInterface.Create(serviceInstance)
		} else {
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("unable to get service instance %s", getErr))
			serviceInstanceStream <- regionEnv
			return
		}
		if updateErr != nil {
			regionEnv.Errors = append(regionEnv.Errors,
				fmt.Errorf("unable to create/update service instance %s\n%s", name, updateErr),
			)
			serviceInstanceStream <- regionEnv
			return
		}

		if serviceInstanceUpdate != nil {
			log.Infof("Update: %+v", serviceInstanceUpdate)
			serviceInstanceSuccessful, watchErr = regionEnv.watchServiceInstance(serviceInstanceUpdate)
		} else {
			// Ensure existing service instance is healthy.
			serviceInstanceSuccessful, watchErr = regionEnv.watchServiceInstance(serviceInstance)
		}

		if watchErr != nil {
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("service instance failure %s", watchErr))
		} else if !serviceInstanceSuccessful {
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("unknown service instance error"))
		}
	}
	select {
	case <-done:
		log.Info("Received Done")
		return
	case serviceInstanceStream <- regionEnv:
	}
}

// updateServiceBindingRegion Creates/updates a service binding.
func updateServiceBindingRegion(
	done chan interface{},
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	serviceBindingStream chan *RegionEnv,
) {
	defer wg.Done()

	// Cluster configmap must have the "cloud" key for service bindings to be created.
	cloud, ok := regionEnv.ClusterSettings["CLOUD"]
	if !ok {
		log.Error("Cannot create or update Service Bindings in %s, as the porter2k8s config map does not have a " +
			"'cloud' key")
		serviceBindingStream <- regionEnv
		return
	}

	// Catalog Service Bindings are not required.
	if len(regionEnv.ServiceBindings[cloud]) == 0 {
		serviceBindingStream <- regionEnv
		return
	}

	var updateErr, getErr, watchErr error
	var serviceBindingSuccessful bool
	var previousServiceBinding, serviceBindingUpdate *catalogv1beta1.ServiceBinding
	// Clientset is region specific.
	serviceBindingInterface := regionEnv.CatalogClientset.ServicecatalogV1beta1().ServiceBindings(
		regionEnv.Cfg.Namespace)

	for _, serviceBinding := range regionEnv.ServiceBindings[cloud] {

		name := serviceBinding.ObjectMeta.Name

		// Check if serviceBinding already exists.
		previousServiceBinding, getErr = serviceBindingInterface.Get(name, metav1.GetOptions{})

		// Update podDisruptionBudget if required.
		if getErr == nil {
			if needsUpdate(previousServiceBinding, serviceBinding) {
				// Resource Version is required to be passed back unaltered.
				serviceBinding.ObjectMeta.ResourceVersion = previousServiceBinding.ObjectMeta.ResourceVersion
				serviceBindingUpdate, updateErr = serviceBindingInterface.Update(serviceBinding)
			}
		} else if errors.IsNotFound(getErr) {
			serviceBindingUpdate, updateErr = serviceBindingInterface.Create(serviceBinding)
		} else {
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("unable to get service binding %s", getErr))
			serviceBindingStream <- regionEnv
			return
		}
		if updateErr != nil {
			regionEnv.Errors = append(
				regionEnv.Errors,
				fmt.Errorf("unable to create/update service binding %s\n%s", name, updateErr),
			)
			serviceBindingStream <- regionEnv
			return
		}

		if serviceBindingUpdate != nil {
			log.Infof("Update: %+v", serviceBindingUpdate)
			serviceBindingSuccessful, watchErr = regionEnv.watchServiceBinding(serviceBindingUpdate)
		} else {
			// Ensure existing service binding is healthy.
			serviceBindingSuccessful, watchErr = regionEnv.watchServiceBinding(serviceBinding)
		}

		if watchErr != nil {
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("service binding failure %s", watchErr))
		} else if !serviceBindingSuccessful {
			regionEnv.Errors = append(regionEnv.Errors, fmt.Errorf("unknown service binding error"))
		}
	}
	select {
	case <-done:
		log.Info("Received Done")
		return
	case serviceBindingStream <- regionEnv:
	}
}
