package porter2k8s

import (
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var createOpts metav1.CreateOptions = metav1.CreateOptions{FieldManager: "porter2k8s"}
var updateOpts metav1.UpdateOptions = metav1.UpdateOptions{FieldManager: "porter2k8s"}

// runUpdate Creates/Updates objects across regions.
func runUpdate(regionEnvs []*RegionEnv, updateFunc UpdateFn) error {

	// See if all regions made it here.
	log.Debugf("Number of regions: %d", len(regionEnvs))

	updater := func() <-chan *RegionEnv {
		// WaitGroup for all of the updates.
		var wg sync.WaitGroup
		wg.Add(len(regionEnvs))
		objectStream := make(chan *RegionEnv)
		for _, regionEnv := range regionEnvs {
			go updateFunc(&wg, regionEnv, objectStream)
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
			return fmt.Errorf("Update failed: %s", update.Errors)
		}
	}

	return nil
}

// updateDeploymentRegion creates/updates and monitors a deployment.
func updatePodObjectRegion(
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	deploymentStream chan *RegionEnv,
) {
	defer wg.Done()

	// Replace docker registry with regional value.
	regionEnv.updateRegistry()
	// Add sidecar if one exists in the cluster settings.
	regionEnv.addSidecarContainer()
	// Inject service binding secrets.
	regionEnv.injectSecretKeyRefs()

	podLogSinker := regionEnv.Cfg.PodLogSinker
	if regionEnv.Cfg.LogMode == "single" {
		regionEnv.Cfg.LogOnce.Do(func() { podLogSinker = LogrusSink() })
	}

	podLogger, logErr := streamPodLogs(
		regionEnv.Context,
		regionEnv.Clientset.CoreV1().Pods(regionEnv.Cfg.Namespace),
		fmt.Sprintf("app=%s,sha=%s", regionEnv.PodObject.GetName(), regionEnv.PodObject.GetLabels()["sha"]),
		regionEnv.PodObject.GetName(),
		podLogSinker)
	if logErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, logErr)
	}

	if len(regionEnv.Errors) > 1 {
		deploymentStream <- regionEnv
		return
	}
	defer podLogger.Stop(1 * time.Second) // cap pod log streaming 1 second after update returns.

	objectStream := make(chan *unstructured.Unstructured)
	defer close(objectStream)
	go dynamicUpdate(nil, regionEnv.PodObject, regionEnv, objectStream)
	update := <-objectStream

	if len(regionEnv.Errors) > 0 {
		deploymentStream <- regionEnv
		return
	}

	regionEnv.Logger.Debugf("Update of Pod Object completed: %+v", update)

	if cleanupErr := regionEnv.postDeployCleanup(); cleanupErr != nil {
		regionEnv.Errors = append(regionEnv.Errors, cleanupErr)
	}

	select {
	case <-regionEnv.Context.Done():
		regionEnv.Logger.Infof("Received Done")
		return
	case deploymentStream <- regionEnv:
	}

}

func updateDynamicServiceRegion(
	wg *sync.WaitGroup,
	regionEnv *RegionEnv,
	serviceStream chan *RegionEnv,
) {
	defer wg.Done()

	// Replace domain name with regional value.
	// This affects all objects with domain names in them, not just services.
	// Since the dynamic update is mandatory, we do it here rather than in istio or ingress.
	regionEnv.updateDomainName()

	// Increase minimum replica number if required.
	regionEnv.updateHPAMinimum()

	regionEnv.Logger.Debugf("Provisioning Dynamic Objects: %+v", regionEnv.Unstructured)
	regionEnv.sortUnstructured()

	if regionEnv.Cfg.DynamicParallel {
		// Create/update all Kubernetes objects at once.
		// TODO: Use dependency metadata to defer creation of dependent objects, then make Dynamic Parallel the default.
		regionEnv.Logger.Info("Provisioning Dynamic Objects in parallel")
		updater := func() <-chan *unstructured.Unstructured {
			var dynamicWg sync.WaitGroup
			dynamicWg.Add(len(regionEnv.Unstructured))
			objectStream := make(chan *unstructured.Unstructured)
			for _, serviceObject := range regionEnv.Unstructured {
				go dynamicUpdate(&dynamicWg, serviceObject, regionEnv, objectStream)
			}
			go func() {
				dynamicWg.Wait()
				close(objectStream)
			}()
			return objectStream
		}

		updates := updater()

		for update := range updates {
			regionEnv.Logger.Debugf("Update of Dynamic Objects completed: %+v", update)
		}
	} else {
		// Create/update all Kubernetes objects sequentially,
		regionEnv.Logger.Info("Provisioning Dynamic Objects sequentially")
		for _, serviceObject := range regionEnv.Unstructured {
			objectStream := make(chan *unstructured.Unstructured)
			go dynamicUpdate(nil, serviceObject, regionEnv, objectStream)
			update := <-objectStream
			close(objectStream)
			regionEnv.Logger.Debugf("Update of Dynamic Object completed: %+v", update)
		}

		select {
		case <-regionEnv.Context.Done():
			regionEnv.Logger.Infof("Received Done")
			return
		case serviceStream <- regionEnv:
		}
	}
}

func dynamicUpdate(
	wg *sync.WaitGroup,
	service *unstructured.Unstructured,
	regionEnv *RegionEnv,
	serviceStream chan *unstructured.Unstructured,
) {
	if wg != nil {
		defer wg.Done()
	}

	var updateErr, getErr error
	var update *unstructured.Unstructured

	gvk := service.GetObjectKind().GroupVersionKind()
	// Token Substitution
	regionEnv.replaceDynamicParameters(service)

	name := service.GetName()
	logger := log.WithFields(log.Fields{"Region": regionEnv.Region, "Name": name, "Kind": gvk.Kind})

	// Find GVR
	mapping, mapperErr := regionEnv.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if mapperErr != nil {
		regionEnv.errf("Unable to get REST Mapping for gvk: %s\n%s", gvk, mapperErr)
		serviceStream <- service
		return
	}

	dynamicInterface := regionEnv.DynamicClient.Resource(mapping.Resource).Namespace(regionEnv.Cfg.Namespace)

	previous, getErr := dynamicInterface.Get(regionEnv.Context, name, metav1.GetOptions{})

	if service.GetKind() == "Job" && getErr == nil {
		logger.Infof("Found existing job %s in %s - trying to delete", name, regionEnv.Region)
		if deleteErr := regionEnv.deleteJob(previous, true); deleteErr != nil {
			regionEnv.errf("Unable to delete preexisting job %s: %w", name, deleteErr)
			serviceStream <- service
			return
		}
		logger.Info("Job deleted")
		time.Sleep(5 * time.Second)
		// Dummy error to have the subsequent logic create a new job.
		getErr = errors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, name)
	}

	if getErr == nil {
		data, _ := service.MarshalJSON()
		// Dynamic Client allows for Server Side Apply (SSA) patching.
		update, updateErr = dynamicInterface.Patch(regionEnv.Context, name, types.ApplyPatchType, data,
			metav1.PatchOptions{FieldManager: "porter2k8s"})
		if errors.IsConflict(updateErr) {
			forceApply := true
			logger.Debugf("Conflict failure detected on %s %s.  Retrying with force option", gvk.Kind, name)
			update, updateErr = dynamicInterface.Patch(regionEnv.Context, name, types.ApplyPatchType, data,
				metav1.PatchOptions{FieldManager: "porter2k8s", Force: &forceApply})
		}
	} else if errors.IsNotFound(getErr) {
		update, updateErr = dynamicInterface.Create(regionEnv.Context, service, createOpts)
	} else {
		regionEnv.errf("Unable to get service %s", gvk.Kind, name, getErr)
		serviceStream <- service
		return
	}

	if updateErr != nil {
		regionEnv.errf("Unable to create/update service %s %s\n%s", gvk.Kind, name, updateErr)
		serviceStream <- service
		return
	}
	logger.Debugf("update: %+v", update)

	watchConfig := NewWatchConfig(service, previous, int32(regionEnv.Cfg.Wait))
	watchConfig.Watch(regionEnv, dynamicInterface)

	regionEnv.createObjectSecrets(update, 0)

	if regionEnv.PodObject.GetKind() == "Job" {
		if deleteErr := regionEnv.deleteJob(update, false); deleteErr != nil {
			regionEnv.Errors = append(regionEnv.Errors, deleteErr)
		}
	}

	select {
	case <-regionEnv.Context.Done():
		logger.Info("Received Done")
		return
	case serviceStream <- service:
	}
}
