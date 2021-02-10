/*
© 2021 Red Hat, Inc. and others

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
package fake

import (
	"time"

	. "github.com/onsi/gomega"
)

type Handler struct {
	gatewayToNonGatewayTransition chan bool
	nonGatewayCleanup             chan bool
}

func New() *Handler {
	return &Handler{
		gatewayToNonGatewayTransition: make(chan bool, 50),
		nonGatewayCleanup:             make(chan bool, 50),
	}
}

func (h *Handler) GetName() string {
	return "fake"
}

func (h *Handler) GatewayToNonGatewayTransition() error {
	h.gatewayToNonGatewayTransition <- true
	return nil
}

func (h *Handler) NonGatewayCleanup() error {
	h.nonGatewayCleanup <- true
	return nil
}

func (h *Handler) AwaitGatewayToNonGatewayTransition() {
	Eventually(h.gatewayToNonGatewayTransition, 5).Should(Receive())
}

func (h *Handler) AwaitNoGatewayToNonGatewayTransition() {
	Consistently(h.gatewayToNonGatewayTransition, 300*time.Millisecond).ShouldNot(Receive())
}

func (h *Handler) AwaitNonGatewayCleanup() {
	Eventually(h.nonGatewayCleanup, 5).Should(Receive())
}

func (h *Handler) AwaitNoNonGatewayCleanup() {
	Consistently(h.nonGatewayCleanup, 300*time.Millisecond).ShouldNot(Receive())
}
