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
	"io"
	"testing"
	"time"

	"github.com/openconfig/ondatra"
	"github.com/openconfig/ygot/ygot"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// WaitForGNMI polls the DUT until a gNMI Subscribe ONCE succeeds or the timeout
// expires. It returns false on timeout.
func WaitForGNMI(t *testing.T, dut *ondatra.DUTDevice, timeout time.Duration) bool {
	t.Helper()
	gnmiC := dut.RawAPIs().GNMI(t)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if gnmiReachable(gnmiC) {
			return true
		}
		time.Sleep(10 * time.Second)
	}
	return false
}

// WaitForGNMIDown polls the DUT until a gNMI Subscribe ONCE fails, indicating
// it has started rebooting. It returns false if the DUT stayed reachable.
func WaitForGNMIDown(t *testing.T, dut *ondatra.DUTDevice, timeout time.Duration) bool {
	t.Helper()
	gnmiC := dut.RawAPIs().GNMI(t)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !gnmiReachable(gnmiC) {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

func gnmiReachable(gnmiC gpb.GNMIClient) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, _ := ygot.StringToStructuredPath("/system/state/hostname")
	sub, err := gnmiC.Subscribe(ctx)
	if err != nil {
		return false
	}
	if err := sub.Send(&gpb.SubscribeRequest{Request: &gpb.SubscribeRequest_Subscribe{Subscribe: &gpb.SubscriptionList{
		Mode:         gpb.SubscriptionList_ONCE,
		Encoding:     gpb.Encoding_PROTO,
		Subscription: []*gpb.Subscription{{Path: p}},
	}}}); err != nil {
		return false
	}
	for {
		resp, err := sub.Recv()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if resp.GetSyncResponse() {
			return true
		}
	}
}
