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

// RawOnce issues a raw gNMI Subscribe ONCE for path using the given encoding
// and returns every update received before sync_response. It is used for
// checks that need to see the exact wire encoding, or paths that are not in the
// generated OpenConfig API.
func RawOnce(t testing.TB, dut *ondatra.DUTDevice, path string, enc gpb.Encoding) []*gpb.Update {
	t.Helper()
	p, err := ygot.StringToStructuredPath(path)
	if err != nil {
		t.Fatalf("StringToStructuredPath(%q): %v", path, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sub, err := dut.RawAPIs().GNMI(t).Subscribe(ctx)
	if err != nil {
		t.Fatalf("gNMI Subscribe: %v", err)
	}
	req := &gpb.SubscribeRequest{Request: &gpb.SubscribeRequest_Subscribe{Subscribe: &gpb.SubscriptionList{
		Mode:         gpb.SubscriptionList_ONCE,
		Encoding:     enc,
		Subscription: []*gpb.Subscription{{Path: p}},
	}}}
	if err := sub.Send(req); err != nil {
		t.Fatalf("gNMI Subscribe Send: %v", err)
	}
	var out []*gpb.Update
	for {
		resp, err := sub.Recv()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("gNMI Subscribe Recv for %q: %v", path, err)
		}
		if resp.GetSyncResponse() {
			return out
		}
		// Fold the notification prefix into each update path so callers can
		// read keys regardless of how the device splits prefix and path.
		prefix := resp.GetUpdate().GetPrefix().GetElem()
		for _, u := range resp.GetUpdate().GetUpdate() {
			if len(prefix) > 0 {
				p := &gpb.Path{Origin: u.GetPath().GetOrigin(), Elem: append(append([]*gpb.PathElem{}, prefix...), u.GetPath().GetElem()...)}
				u = &gpb.Update{Path: p, Val: u.GetVal(), Duplicates: u.GetDuplicates()}
			}
			out = append(out, u)
		}
	}
}
