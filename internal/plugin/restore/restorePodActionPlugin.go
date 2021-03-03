/*
Copyright 2018, 2019 the Velero contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package plugin

import (
	util "catalogicsoftware.com/velero-plugin/internal/plugin/util"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/vmware-tanzu/velero/pkg/plugin/framework"
	"github.com/vmware-tanzu/velero/pkg/plugin/velero"
	corev1api "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	//RestorePodActionPluginName puts a name to this particular plugin
	RestorePodActionPluginName = "catalogicsoftware.com/offload-restore-pod-action-plugin"
)

// RestorePodActionPlugin is a restore item action plugin for Velero
type RestorePodActionPlugin struct {
	log logrus.FieldLogger
}

type initcontainerConfig struct {
	clusterID                string
	kubeMoverPodNamePrefix   string
	kubeMoverImage           string
	serverAddr               string
	useTLS                   string
	cpuRequest               string
	cpuLimit                 string
	memRequest               string
	memLimit                 string
	runAsRoot                string
	runAsGroup               string
	allowPrivilegeEscalation string
}

func newInitcontainerConfig() (*initcontainerConfig, error) {
	client, err := util.GetClients()
	if err != nil {
		return nil, err
	}

	configClientSet := client.CoreV1().ConfigMaps("cloudcasa-io")
	config, err := util.GetPluginConfig(
		framework.PluginKindRestoreItemAction,
		RestorePodActionPluginName,
		configClientSet,
	)
	if err != nil {
		return nil, err
	}

	i := initcontainerConfig{
		clusterID:                config.Data["clusterID"],
		kubeMoverPodNamePrefix:   config.Data["kubeMoverPodNamePrefix"],
		kubeMoverImage:           config.Data["kubeMoverImage"],
		serverAddr:               config.Data["serverAddr"],
		useTLS:                   config.Data["useTLS"],
		runAsRoot:                config.Data["runAsRoot"],
		runAsGroup:               config.Data["runAsGroup"],
		allowPrivilegeEscalation: config.Data["allowPrivilegeEscalation"],
	}
	if config.Data["cpuRequest"] == "" {
		i.cpuRequest = "100m"
	}
	if config.Data["cpuLimit"] == "" {
		i.cpuLimit = "128Mi"
	}
	if config.Data["memRequest"] == "" {
		i.memRequest = "100m"
	}
	if config.Data["memLimit"] == "" {
		i.memLimit = "128Mi"
	}
	return &i, nil
}

// NewRestorePodActionPlugin instantiates a RestorePlugin.
func NewRestorePodActionPlugin(log logrus.FieldLogger) *RestorePodActionPlugin {

	return &RestorePodActionPlugin{
		log: log,
	}
}

// AppliesTo returns information about which resources this action should be invoked for.
// A RestoreItemAction's Execute function will only be invoked on items that match the returned
// selector. A zero-valued ResourceSelector matches all resources.g
func (p *RestorePodActionPlugin) AppliesTo() (velero.ResourceSelector, error) {
	return velero.ResourceSelector{
		IncludedResources: []string{"pods"},
	}, nil
}

// Execute allows the RestorePlugin to perform arbitrary logic with the item being restored,
// in this case, setting a custom annotation on the item being restored.
func (p *RestorePodActionPlugin) Execute(input *velero.RestoreItemActionExecuteInput) (*velero.RestoreItemActionExecuteOutput, error) {
	p.log.Info("catalogicsoftware.com/offload-restore-pod-action-plugin!")

	restoreObjectAnnotations := input.Restore.GetAnnotations()
	if restoreObjectAnnotations == nil {
		return velero.NewRestoreItemActionExecuteOutput(input.Item), nil
	}
	if restoreObjectAnnotations != nil {
		if _, ok := restoreObjectAnnotations["cloudcasa-restore-from-offload"]; !ok {
			return velero.NewRestoreItemActionExecuteOutput(input.Item), nil
		}
	}

	metadata, err := meta.Accessor(input.Item)
	if err != nil {
		return &velero.RestoreItemActionExecuteOutput{}, err
	}

	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations["catalogicsoftware.com/offload-restore-pod-action-plugin"] = "1"
	metadata.SetAnnotations(annotations)

	//// Get the marshalled pod object to be restored
	var pod corev1api.Pod
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(input.Item.UnstructuredContent(), &pod); err != nil {
		return nil, errors.Wrap(err, "Unable to convert unstructured item to pod")
	}

	var podVolumes []corev1api.Volume
	var podVolumeMounts []corev1api.VolumeMount
	var podMountPoints []string

	// Create a list of volumes to be mounted on the KubeMover Pod
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}

		claimName := volume.PersistentVolumeClaim.ClaimName
		podVolumes = append(podVolumes, corev1api.Volume{
			Name: volume.Name,
			VolumeSource: corev1api.VolumeSource{
				PersistentVolumeClaim: &corev1api.PersistentVolumeClaimVolumeSource{
					ClaimName: claimName,
					ReadOnly:  false,
				},
			},
		})

		mountPath := "/" + claimName

		podVolumeMounts = append(podVolumeMounts, corev1api.VolumeMount{
			Name:      volume.Name,
			MountPath: mountPath,
			ReadOnly:  false,
		})

		podMountPoints = append(podMountPoints, mountPath)

		p.log.Infof("Adding PVC %s/%s as an item to restored from offloaded data", pod.Namespace, volume.PersistentVolumeClaim.ClaimName)
	}
	initContainerConfig, err := newInitcontainerConfig()

	resourceReqs, err := util.ParseResourceRequirements(initContainerConfig.cpuRequest, initContainerConfig.memRequest, initContainerConfig.cpuLimit, initContainerConfig.memLimit)
	securityContext, err := util.ParseSecurityContext(initContainerConfig.runAsRoot, initContainerConfig.runAsGroup, initContainerConfig.allowPrivilegeEscalation)
	initcontainerName := initContainerConfig.kubeMoverPodNamePrefix + pod.Name
	initContainer := corev1api.Container{
		Name:  initcontainerName,
		Image: initContainerConfig.kubeMoverImage,
		Env: []corev1api.EnvVar{
			{
				Name: "AMDS_CLUSTER_ID", Value: initContainerConfig.clusterID,
			},
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &corev1api.EnvVarSource{
					FieldRef: &corev1api.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
			{
				Name: "POD_NAME",
				ValueFrom: &corev1api.EnvVarSource{
					FieldRef: &corev1api.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
		},
		Args: append(
			[]string{
				"/usr/local/bin/kubemover",
				"--server_addr", initContainerConfig.serverAddr,
				"--tls", initContainerConfig.useTLS,
			},
			podMountPoints...,
		),
		VolumeMounts:    podVolumeMounts,
		Resources:       resourceReqs,
		SecurityContext: &securityContext,
	}

	if len(pod.Spec.InitContainers) == 0 || pod.Spec.InitContainers[0].Name != initcontainerName {
		pod.Spec.InitContainers = append([]corev1api.Container{initContainer}, pod.Spec.InitContainers...)
	} else {
		pod.Spec.InitContainers[0] = initContainer
	}

	res, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&pod)
	if err != nil {
		return nil, errors.Wrap(err, "unable to convert pod to runtime.Unstructured")
	}

	return velero.NewRestoreItemActionExecuteOutput(&unstructured.Unstructured{Object: res}), nil

}
