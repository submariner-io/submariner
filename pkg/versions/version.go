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

package versions

import (
	"fmt"
	"runtime"

	"github.com/submariner-io/admiral/pkg/log"
)

var (
	version       = "devel"
	gitCommitHash string
	gitCommitDate string
)

func Log(logger *log.Logger) {
	logger.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	logger.Info(fmt.Sprintf("Go Arch: %s", runtime.GOARCH))
	logger.Info(fmt.Sprintf("Git Commit Hash: %s", gitCommitHash))
	logger.Info(fmt.Sprintf("Git Commit Date: %s", gitCommitDate))
}

// Submariner returns the version info of submariner.
func Submariner() string {
	return version
}
