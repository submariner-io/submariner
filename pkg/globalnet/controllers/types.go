/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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
package controllers

import (
	"sync"

	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/ipam"
)

type Interface interface {
	Start() error
	Stop()
}

type globalEgressIPController struct {
	sync.Mutex
	pool           *ipam.IPPool
	podWatchers    map[string]*podWatcher
	watcherConfig  watcher.Config
	resourceSyncer syncer.Interface
	stopCh         chan struct{}
}

type podWatcher struct {
	stopCh chan struct{}
}
