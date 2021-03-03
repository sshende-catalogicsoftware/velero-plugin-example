/*
Copyright 2020 the Velero contributors.

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

package util

import (
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	"github.com/vmware-tanzu/velero/pkg/label"
	"github.com/vmware-tanzu/velero/pkg/plugin/framework"
	corev1api "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

// GetPVForPVC returns a PV object bound to a PVC
func GetPVForPVC(pvc *corev1api.PersistentVolumeClaim, corev1 corev1client.PersistentVolumesGetter) (*corev1api.PersistentVolume, error) {
	if pvc.Spec.VolumeName == "" {
		return nil, errors.Errorf("PVC %s/%s has no volume backing this claim", pvc.Namespace, pvc.Name)
	}
	if pvc.Status.Phase != corev1api.ClaimBound {
		// TODO: confirm if this PVC should be snapshotted if it has no PV bound
		return nil, errors.Errorf("PVC %s/%s is in phase %v and is not bound to a volume", pvc.Namespace, pvc.Name, pvc.Status.Phase)
	}
	pvName := pvc.Spec.VolumeName
	pv, err := corev1.PersistentVolumes().Get(pvName, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get PV %s for PVC %s/%s", pvName, pvc.Namespace, pvc.Name)
	}
	return pv, nil
}

//GetPodsUsingPVC lists all pods where this PVC is used
func GetPodsUsingPVC(pvcNamespace, pvcName string, corev1 corev1client.PodsGetter) ([]corev1api.Pod, error) {
	podsUsingPVC := []corev1api.Pod{}
	podList, err := corev1.Pods(pvcNamespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, p := range podList.Items {
		for _, v := range p.Spec.Volumes {
			if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == pvcName {
				podsUsingPVC = append(podsUsingPVC, p)
			}
		}
	}

	return podsUsingPVC, nil
}

// GetPodVolumeNameForPVC returns the volume name in a POD spec for a particular PVC
func GetPodVolumeNameForPVC(pod corev1api.Pod, pvcName string) (string, error) {
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == pvcName {
			return v.Name, nil
		}
	}
	return "", errors.Errorf("Pod %s/%s does not use PVC %s/%s", pod.Namespace, pod.Name, pod.Namespace, pvcName)
}

// Contains checks if a key is present in a slice
func Contains(slice []string, key string) bool {
	for _, i := range slice {
		if i == key {
			return true
		}
	}
	return false
}

// GetClients creates and returns a kubernetes clientset
func GetClients() (*kubernetes.Clientset, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	clientConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	client, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return client, nil
}

// AddAnnotations adds the supplied key-values to the annotations on the object
func AddAnnotations(o *metav1.ObjectMeta, vals map[string]string) {
	if o.Annotations == nil {
		o.Annotations = make(map[string]string)
	}
	for k, v := range vals {
		o.Annotations[k] = v
	}
}

// AddLabels adds the supplied key-values to the labels on the object
func AddLabels(o *metav1.ObjectMeta, vals map[string]string) {
	if o.Labels == nil {
		o.Labels = make(map[string]string)
	}
	for k, v := range vals {
		o.Labels[k] = label.GetValidName(v)
	}
}

// UpdatePvAnnotation updates the Persistent Volume Resource
func UpdatePvAnnotation(key, value, pvcName string, client *kubernetes.Clientset) error {
	pvClient := client.CoreV1().PersistentVolumes()
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Retrieve the latest version of PV before attempting update
		// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		result, getErr := client.CoreV1().PersistentVolumes().Get(pvcName, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		result.GetAnnotations()[key] = value
		_, updateErr := pvClient.Update(result)
		return updateErr
	})
	if retryErr != nil {
		return retryErr
	}
	return nil
}

// ParseResourceRequirements takes a set of CPU and memory requests and limit string
// values and returns a ResourceRequirements struct to be used in a Container.
// An error is returned if we cannot parse the request/limit.
func ParseResourceRequirements(cpuRequest, memRequest, cpuLimit, memLimit string) (corev1api.ResourceRequirements, error) {
	resources := corev1api.ResourceRequirements{
		Requests: corev1api.ResourceList{},
		Limits:   corev1api.ResourceList{},
	}

	parsedCPURequest, err := resource.ParseQuantity(cpuRequest)
	if err != nil {
		return resources, errors.Wrapf(err, `couldn't parse CPU request "%s"`, cpuRequest)
	}

	parsedMemRequest, err := resource.ParseQuantity(memRequest)
	if err != nil {
		return resources, errors.Wrapf(err, `couldn't parse memory request "%s"`, memRequest)
	}

	parsedCPULimit, err := resource.ParseQuantity(cpuLimit)
	if err != nil {
		return resources, errors.Wrapf(err, `couldn't parse CPU limit "%s"`, cpuLimit)
	}

	parsedMemLimit, err := resource.ParseQuantity(memLimit)
	if err != nil {
		return resources, errors.Wrapf(err, `couldn't parse memory limit "%s"`, memLimit)
	}

	// A quantity of 0 is treated as unbounded
	unbounded := resource.MustParse("0")

	if parsedCPULimit != unbounded && parsedCPURequest.Cmp(parsedCPULimit) > 0 {
		return resources, errors.WithStack(errors.Errorf(`CPU request "%s" must be less than or equal to CPU limit "%s"`, cpuRequest, cpuLimit))
	}

	if parsedMemLimit != unbounded && parsedMemRequest.Cmp(parsedMemLimit) > 0 {
		return resources, errors.WithStack(errors.Errorf(`Memory request "%s" must be less than or equal to Memory limit "%s"`, memRequest, memLimit))
	}

	// Only set resources if they are not unbounded
	if parsedCPURequest != unbounded {
		resources.Requests[corev1api.ResourceCPU] = parsedCPURequest
	}
	if parsedMemRequest != unbounded {
		resources.Requests[corev1api.ResourceMemory] = parsedMemRequest
	}
	if parsedCPULimit != unbounded {
		resources.Limits[corev1api.ResourceCPU] = parsedCPULimit
	}
	if parsedMemLimit != unbounded {
		resources.Limits[corev1api.ResourceMemory] = parsedMemLimit
	}

	return resources, nil
}

func ParseSecurityContext(runAsUser string, runAsGroup string, allowPrivilegeEscalation string) (corev1api.SecurityContext, error) {
	securityContext := corev1api.SecurityContext{}

	if runAsUser != "" {
		parsedRunAsUser, err := strconv.ParseInt(runAsUser, 10, 64)
		if err != nil {
			return securityContext, errors.WithStack(errors.Errorf(`Security context runAsUser "%s" is not a number`, runAsUser))
		}

		securityContext.RunAsUser = &parsedRunAsUser
	}

	if runAsGroup != "" {
		parsedRunAsGroup, err := strconv.ParseInt(runAsGroup, 10, 64)
		if err != nil {
			return securityContext, errors.WithStack(errors.Errorf(`Security context runAsGroup "%s" is not a number`, runAsGroup))
		}

		securityContext.RunAsGroup = &parsedRunAsGroup
	}

	if allowPrivilegeEscalation != "" {
		parsedAllowPrivilegeEscalation, err := strconv.ParseBool(allowPrivilegeEscalation)
		if err != nil {
			return securityContext, errors.WithStack(errors.Errorf(`Security context allowPrivilegeEscalation "%s" is not a boolean`, allowPrivilegeEscalation))
		}

		securityContext.AllowPrivilegeEscalation = &parsedAllowPrivilegeEscalation
	}

	return securityContext, nil
}

func GetPluginConfig(kind framework.PluginKind, name string, client corev1client.ConfigMapInterface) (*corev1api.ConfigMap, error) {
	opts := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("velero.io/plugin-config,%s=%s", name, kind),
	}

	list, err := client.List(opts)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if len(list.Items) == 0 {
		return nil, nil
	}

	if len(list.Items) > 1 {
		var items []string
		for _, item := range list.Items {
			items = append(items, item.Name)
		}
		return nil, errors.Errorf("found more than one ConfigMap matching label selector %q: %v", opts.LabelSelector, items)
	}

	return &list.Items[0], nil
}
