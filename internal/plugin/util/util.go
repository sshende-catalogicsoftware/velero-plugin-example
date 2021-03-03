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
	"context"
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	"github.com/vmware-tanzu/velero/pkg/plugin/framework"
	corev1api "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

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

// ParseSecurityContext will parse the security context related string values from configmap
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

// GetPluginConfig reads the configmap that contains config params for this plugin
func GetPluginConfig(kind framework.PluginKind, name string, client corev1client.ConfigMapInterface) (*corev1api.ConfigMap, error) {
	opts := metav1.ListOptions{
		// velero.io/plugin-config: true
		// velero.io/restic: RestoreItemAction
		LabelSelector: fmt.Sprintf("velero.io/plugin-config,%s=%s", name, kind),
	}

	list, err := client.List(context.TODO(), opts)
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
