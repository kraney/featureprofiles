// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mlc

import (
	"context"
	"testing"
	"time"

	"github.com/openconfig/ondatra"
	"github.com/openconfig/ygot/ygot"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// WaitForGNMI polls the DUT until a gNMI Get succeeds or the timeout expires.
// It returns false on timeout.
func WaitForGNMI(t *testing.T, dut *ondatra.DUTDevice, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if gnmiReachable(t, dut) {
			return true
		}
		time.Sleep(10 * time.Second)
	}
	return false
}

// WaitForGNMIDown polls the DUT until a gNMI Get fails, indicating it has
// started rebooting. It returns false if the DUT stayed reachable.
func WaitForGNMIDown(t *testing.T, dut *ondatra.DUTDevice, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !gnmiReachable(t, dut) {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func gnmiReachable(t *testing.T, dut *ondatra.DUTDevice) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, _ := ygot.StringToStructuredPath("/system/state/hostname")
	_, err := dut.RawAPIs().GNMI(t).Get(ctx, &gpb.GetRequest{Path: []*gpb.Path{p}, Encoding: gpb.Encoding_JSON_IETF})
	return err == nil
}
