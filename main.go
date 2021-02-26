/*
Copyright 2017, 2019 the Velero contributors.

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

package main

import (
	"github.com/sirupsen/logrus"
	"github.com/vmware-tanzu/velero-plugin-example/internal/plugin"
	"github.com/vmware-tanzu/velero/pkg/plugin/framework"
)

func main() {
	framework.NewServer().
		RegisterRestoreItemAction("catalogicsoftware.com/offload-restore-pvc-action-plugin", newRestorePvcActionPlugin).Serve()

	framework.NewServer().
		RegisterRestoreItemAction("catalogicsoftware.com/offload-restore-pod-action-plugin", newRestorePvcActionPlugin).Serve()
}

func newRestorePvcActionPlugin(logger logrus.FieldLogger) (interface{}, error) {
	return plugin.NewRestorePvcActionPlugin(logger), nil
}

func newRestorePodActionPlugin(logger logrus.FieldLogger) (interface{}, error) {
	return plugin.NewRestorePodActionPlugin(logger), nil
}
