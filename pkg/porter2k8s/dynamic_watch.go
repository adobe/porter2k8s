/*
Copyright 2021 Adobe. All rights reserved.
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
	"strings"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// WatchConfig is for customized watches of unstructured objects.
type WatchConfig struct {
	Failure        WatchMatch
	Kind           string
	Name           string
	Service        *unstructured.Unstructured
	Success        WatchMatch
	TimeoutSeconds *int64
}

// WatchMatch are combinations of conditions for watch success or failure.
type WatchMatch struct {
	Conditions []WatchCondition
	Logic      ConditionLogic
}

// WatchCondition are conditions for watch success or failure.
type WatchCondition struct {
	Fields      []string // Passed to unstructured helpers.
	StateBool   bool
	StateInt64  int64
	StateString string
	StateMap    map[string]interface{}
	Type        ConditionType
}

// ConditionType type of comparison for WatchCondition
type ConditionType int

// Further comparison types can be added.
// Anything in k8s.io/apimachinery@v0.17.2/pkg/apis/meta/v1/unstructured/helpers.go could be supported.
const (
	BoolCondition           ConditionType = 0
	Int64Condition          ConditionType = 1
	StringCondition         ConditionType = 2
	StringContainsCondition ConditionType = 3
	StringSliceCondition    ConditionType = 4
	MapCondition            ConditionType = 5
	SliceMapCondition       ConditionType = 6 // Slice of Maps
)

// ConditionLogic type of comparison for WatchCondition
type ConditionLogic int

// Logical operators for combinations of Watch Conditions
// Further logical operators types can be added.
const (
	AndLogic ConditionLogic = 0
	OrLogic  ConditionLogic = 1
)

// NewWatchConfig creates a WatchConfig on a service. Pod objects can wait an additional configurable period.
// TODO: Check for configuration error and log it.
func NewWatchConfig(service *unstructured.Unstructured,
	previous *unstructured.Unstructured,
	podWait int32) WatchConfig {
	watchConfig := WatchConfig{
		Failure:        WatchMatch{Conditions: []WatchCondition{}, Logic: OrLogic},
		Kind:           service.GetKind(),
		Name:           service.GetName(),
		Service:        service,
		Success:        WatchMatch{Conditions: []WatchCondition{}, Logic: AndLogic},
		TimeoutSeconds: int64Ref(0), // Timeout value of zero will indicate that no wait should be set on the object.
	}
	switch service.GetAPIVersion() {
	case "apps/v1":
		watchConfig.TimeoutSeconds = calculateWatchTimeout(service, podWait)
		switch watchConfig.Kind {
		case "Deployment":
			watchConfig.Success.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "conditions"},
					StateMap: map[string]interface{}{"reason": "NewReplicaSetAvailable", "status": "True", "type": "Progressing"},
					Type:     SliceMapCondition},
			}
			watchConfig.Failure.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "conditions"},
					StateMap: map[string]interface{}{"reason": "ProgressDeadlineExceeded", "status": "False", "type": "Progressing"},
					Type:     SliceMapCondition},
			}
		case "StatefulSet":
			if previous == nil {
				previous = service
			}
			currentReplicas, found, err := unstructured.NestedInt64(previous.Object, "spec", "replicas")
			if err != nil || !found {
				log.Warnf("Current replica count not found for statefulSet %s", previous.GetName())
				currentReplicas = 1
			}
			log.Infof("Will wait for %d replicas to become ready", currentReplicas)
			watchConfig.Success.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "readyReplicas"}, StateInt64: currentReplicas,
					Type: Int64Condition},
				WatchCondition{Fields: []string{"status", "updatedReplicas"}, StateInt64: currentReplicas,
					Type: Int64Condition},
				WatchCondition{Fields: []string{"status", "unavailableReplicas"}, StateInt64: 0,
					Type: Int64Condition},
			}
		}
	case "batch/v1":
		// This covers both deployments and stateful set.
		watchConfig.TimeoutSeconds = int64Ref(podWait)
		watchConfig.Success.Conditions = []WatchCondition{
			WatchCondition{Fields: []string{"status", "succeeded"}, StateInt64: 1,
				Type: Int64Condition},
		}
	case "dynamodb.services.k8s.aws/v1alpha1":
		switch watchConfig.Kind {
		case "Table":
			watchConfig.Success.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "tableStatus"}, StateString: "ACTIVE", Type: StringCondition},
			}
			watchConfig.Failure.Conditions = []WatchCondition{
				//TODO: Find better failure condition.
				WatchCondition{Fields: []string{"status", "tableStatus"}, StateString: "DELETING",
					Type: StringCondition},
			}
			watchConfig.TimeoutSeconds = int64Ref(10 * 60)
		}
	case "elasticache.services.k8s.aws/v1alpha1":
		switch watchConfig.Kind {
		//CacheSubnetGroup has no conditions on successful provisioning.
		case "ReplicationGroup":
			watchConfig.Success.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "conditions"},
					StateMap: map[string]interface{}{"type": "ACK.ResourceSynced", "status": "True"},
					Type:     SliceMapCondition},
				WatchCondition{Fields: []string{"status", "status"}, StateString: "available", Type: StringCondition},
			}
			watchConfig.TimeoutSeconds = int64Ref(2 * 60)
		}
	case "azure.microsoft.com/v1alpha1", "azure.microsoft.com/v1alpha2", "azure.microsoft.com/v1beta1":
		// ASO provides standard status messages, at least as of 4/21.
		watchConfig.Success.Conditions = []WatchCondition{
			WatchCondition{Fields: []string{"status", "provisioned"}, StateBool: true, Type: BoolCondition},
		}
		watchConfig.Failure.Conditions = []WatchCondition{
			WatchCondition{Fields: []string{"status", "failedProvisioning"}, StateBool: true, Type: BoolCondition},
			WatchCondition{Fields: []string{"status", "message"}, StateString: "Failure responding to request",
				Type: StringContainsCondition},
		}
		switch watchConfig.Kind {
		case "MySQLServer":
			watchConfig.TimeoutSeconds = int64Ref(30 * 60)
		case "CosmosDB":
			watchConfig.TimeoutSeconds = int64Ref(15 * 60)
		case "RedisCache":
			watchConfig.TimeoutSeconds = int64Ref(15 * 60)
		default:
			watchConfig.TimeoutSeconds = int64Ref(2 * 60)
		}
	case "servicecatalog.k8s.io/v1beta1":
		watchConfig.Success.Conditions = []WatchCondition{
			WatchCondition{Fields: []string{"status", "condition"}, StateString: "Ready", Type: StringSliceCondition},
		}
		switch watchConfig.Kind {
		case "ServiceInstance":
			watchConfig.TimeoutSeconds = int64Ref(50 * 60)
			watchConfig.Failure.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "conditions"},
					StateMap: map[string]interface{}{"type": "Failed", "status": "True"}, Type: SliceMapCondition},
				WatchCondition{Fields: []string{"status", "conditions"},
					StateMap: map[string]interface{}{"type": "OrphanMigration", "status": "True"}, Type: SliceMapCondition},
			}
			watchConfig.Success.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "conditions"},
					StateMap: map[string]interface{}{"type": "Ready", "status": "True"}, Type: SliceMapCondition},
			}
		case "ServiceBinding":
			watchConfig.TimeoutSeconds = int64Ref(30)
			watchConfig.Failure.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "conditions"},
					StateMap: map[string]interface{}{"type": "Failed", "status": "True"}, Type: SliceMapCondition},
			}
			watchConfig.Success.Conditions = []WatchCondition{
				WatchCondition{Fields: []string{"status", "conditions"},
					StateMap: map[string]interface{}{"type": "Ready", "status": "True"}, Type: SliceMapCondition},
			}
		}
	}
	return watchConfig
}

// Watch watches the unstructured objects.
func (watchConfig *WatchConfig) Watch(regionEnv *RegionEnv, dynamicInterface dynamic.ResourceInterface) {
	logger := log.WithFields(log.Fields{"Region": regionEnv.Region, "Name": watchConfig.Name, "Kind": watchConfig.Kind})
	if *watchConfig.TimeoutSeconds == 0 {
		logger.Infof("No watch for objects of type %s/%s, %s", watchConfig.Service.GetAPIVersion(), watchConfig.Kind,
			watchConfig.Name)
		return
	}
	listOptions := metav1.ListOptions{
		FieldSelector:  fields.OneTermEqualSelector("metadata.name", watchConfig.Name).String(),
		TimeoutSeconds: watchConfig.TimeoutSeconds,
	}
	logger.Infof("Waiting %d seconds for %s to be ready", *watchConfig.TimeoutSeconds, watchConfig.Name)

	watcher, watchErr := dynamicInterface.Watch(regionEnv.Context, listOptions)
	if watchErr != nil {
		regionEnv.errf("%s", watchErr)
		return
	}
	watchCh := watcher.ResultChan()
	for {
		select {
		case <-regionEnv.Context.Done():
			logger.Info("Received Done")
			// Returning true to prevent an error from being reported.
			// If SIGTERM was sent, the user doesn't care about the serviceInstance status.
			return
		case event, ok := <-watchCh:
			if !ok {
				regionEnv.errf("timeout watching job")
				return
			}
			switch event.Type {
			case watch.Added, watch.Modified:
				update := event.Object.(*unstructured.Unstructured)
				logger.Debugf("Update: %+v", update)

				status, found, err := unstructured.NestedMap(update.Object, "status")
				if !found {
					log.Debugf("Unable to get service status, waiting for next event: error %s", err)
					continue
				}
				logger.Debugf("Status: %s", status)
				failed := watchConfig.Failure.check(update)
				if failed {
					regionEnv.errf("unable to create/update service %s %s\n%s", watchConfig.Kind,
						watchConfig.Name, status)
					return
				}
				succeeded := watchConfig.Success.check(update)
				if succeeded {
					logger.Infof("Dynamic object %s %s successfully provisioned", watchConfig.Kind,
						watchConfig.Name)
					return
				}

			case watch.Deleted:
				regionEnv.errf("service deleted while watching for completion")
				return

			case watch.Error:
				err := errors.FromObject(event.Object)
				regionEnv.errf("error reported while watching service: %s", err)
				return
			}
		}
	}
}

// check calls watchCondition's check and applies logic.
func (match *WatchMatch) check(update *unstructured.Unstructured) bool {
	matches := 0
	for _, condition := range match.Conditions {
		if condition.check(update) {
			matches++
		}
	}
	if match.Logic == OrLogic && matches > 0 {
		return true
	}
	if match.Logic == AndLogic && matches == len(match.Conditions) {
		return true
	}
	return false
}

// check returns true if the condition is met.
func (condition *WatchCondition) check(update *unstructured.Unstructured) bool {
	switch condition.Type {
	case BoolCondition:
		status, _, _ := unstructured.NestedBool(update.Object, condition.Fields...)
		if status == condition.StateBool {
			return true
		}
	case Int64Condition:
		status, _, _ := unstructured.NestedInt64(update.Object, condition.Fields...)
		if status == condition.StateInt64 {
			return true
		}
	case StringCondition:
		status, _, _ := unstructured.NestedString(update.Object, condition.Fields...)
		if status == condition.StateString {
			return true
		}
	case StringContainsCondition:
		status, _, _ := unstructured.NestedString(update.Object, condition.Fields...)
		if strings.Contains(status, condition.StateString) {
			return true
		}
	case StringSliceCondition:
		status, _, _ := unstructured.NestedStringSlice(update.Object, condition.Fields...)
		for _, message := range status {
			if message == condition.StateString {
				return true
			}
		}
	case MapCondition:
		status, _, _ := unstructured.NestedMap(update.Object, condition.Fields...)
		return compareStatusMaps(status, condition.StateMap)
	case SliceMapCondition:
		statuses, _, _ := unstructured.NestedSlice(update.Object, condition.Fields...)
		for _, status := range statuses {
			statusMap := status.(map[string]interface{})
			if compareStatusMaps(statusMap, condition.StateMap) {
				return true
			}
		}
		return false
	}
	return false
}

func compareStatusMaps(status, conditionMap map[string]interface{}) bool {
	for key, value := range conditionMap {
		if value != status[key] {
			return false
		}
	}
	return true
}
