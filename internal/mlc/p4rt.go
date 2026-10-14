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
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"
	"google.golang.org/grpc/codes"

	p4pb "github.com/p4lang/p4runtime/go/p4/v1"
)

// NodeIDBase is the first P4RT node-id the tests assign to an integrated
// circuit.
const NodeIDBase = 0x4d4c43000000 // "MLC" followed by zeros.

// TargetICs returns the ICs a controller would program: every discovered
// INTEGRATED_CIRCUIT plus the conventionally named chips of every slot that
// holds a DUT port. A controller configures node-id on the chips in its model
// whether or not the device advertised them.
func TargetICs(dut *ondatra.DUTDevice, inv *Inventory, slots []int) []string {
	set := map[string]bool{}
	for _, ic := range inv.ICs() {
		set[ic] = true
	}
	if n, ok := NamingFor(dut); ok {
		for _, s := range slots {
			for c := 0; c < *MinICsPerLinecard; c++ {
				set[n.ICName(s, c)] = true
			}
		}
	}
	var out []string
	for ic := range set {
		out = append(out, ic)
	}
	sort.Strings(out)
	return out
}

// parentLinecard returns the linecard an IC belongs to, from its reported
// parent or else from the vendor naming convention.
func parentLinecard(dut *ondatra.DUTDevice, inv *Inventory, ic string) string {
	if inv != nil {
		if c := inv.Components[ic]; c != nil && c.Parent != "" {
			if p := inv.Components[c.Parent]; p != nil && p.Type == oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_LINECARD.String() {
				return c.Parent
			}
		}
	}
	if n, ok := NamingFor(dut); ok {
		if s, ok := SlotFromName(n.IC, ic); ok {
			return n.LinecardName(s)
		}
	}
	return ""
}

// ConfigureNodeIDs sets a distinct node-id on each IC. Like a controller's
// generated config, it also declares the IC's parent linecard component, since
// some devices reject IC config whose parent component is not configured. ICs
// whose Set is rejected are reported through t.Errorf and omitted from the
// result.
func ConfigureNodeIDs(t *testing.T, dut *ondatra.DUTDevice, inv *Inventory, ics []string) map[string]uint64 {
	t.Helper()
	got := map[string]uint64{}
	for i, ic := range ics {
		id := uint64(NodeIDBase + i + 1)
		// The component list key is a leafref to config/name, so set both.
		d := &oc.Root{}
		d.GetOrCreateComponent(ic).GetOrCreateIntegratedCircuit().NodeId = &id
		if lc := parentLinecard(dut, inv, ic); lc != "" {
			d.GetOrCreateComponent(lc)
		}
		ok := fptest.NonFatal(t, func(t testing.TB) {
			b := &gnmi.SetBatch{}
			for _, c := range d.Component {
				gnmi.BatchUpdate(b, gnmi.OC().Component(*c.Name).Config(), c)
			}
			b.Set(t, dut)
		})
		if !ok {
			t.Errorf("%s: gNMI Set of integrated-circuit/config/node-id rejected", ic)
			continue
		}
		got[ic] = id
	}
	return got
}

// PortID returns a slot-encoded 32-bit interface ID at or above 65536, unique
// per (slot, index) on the chassis.
func PortID(slot, index int) uint32 {
	return uint32(1<<16 | slot<<8 | index)
}

// ConfigurePortIDs assigns slot-encoded interface IDs to every DUT port and
// returns them keyed by interface name.
func ConfigurePortIDs(t *testing.T, dut *ondatra.DUTDevice) map[string]uint32 {
	t.Helper()
	slots := DUTPortSlots(t, dut)
	out := map[string]uint32{}
	b := &gnmi.SetBatch{}
	for i, p := range dut.Ports() {
		// Use the testbed port number (port<N>) so IDs are stable across runs.
		idx := i + 1
		if n, err := strconv.Atoi(strings.TrimPrefix(p.ID(), "port")); err == nil {
			idx = n
		}
		id := PortID(slots[p.Name()], idx)
		out[p.Name()] = id
		gnmi.BatchUpdate(b, gnmi.OC().Interface(p.Name()).Config(), &oc.Interface{
			Name: &[]string{p.Name()}[0],
			Type: oc.IETFInterfaces_InterfaceType_ethernetCsmacd,
			Id:   &id,
		})
	}
	b.Set(t, dut)
	return out
}

// Stream is a P4RT StreamChannel bound to one device_id.
type Stream struct {
	DeviceID uint64
	stream   p4pb.P4Runtime_StreamChannelClient
	cancel   context.CancelFunc
	Arb      chan *p4pb.MasterArbitrationUpdate
	Packets  chan *p4pb.PacketIn
	mu       sync.Mutex
	err      error
	done     chan struct{}
}

// OpenStream opens a StreamChannel and starts a receive loop.
func OpenStream(client p4pb.P4RuntimeClient, deviceID uint64) (*Stream, error) {
	ctx, cancel := context.WithCancel(context.Background())
	sc, err := client.StreamChannel(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	s := &Stream{
		DeviceID: deviceID, stream: sc, cancel: cancel,
		Arb: make(chan *p4pb.MasterArbitrationUpdate, 16), Packets: make(chan *p4pb.PacketIn, 1024),
		done: make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		for {
			m, err := sc.Recv()
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
			switch u := m.GetUpdate().(type) {
			case *p4pb.StreamMessageResponse_Arbitration:
				select {
				case s.Arb <- u.Arbitration:
				default:
				}
			case *p4pb.StreamMessageResponse_Packet:
				select {
				case s.Packets <- u.Packet:
				default:
				}
			}
		}
	}()
	return s, nil
}

// Err returns the error that terminated the stream, if any.
func (s *Stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close closes the stream.
func (s *Stream) Close() {
	s.stream.CloseSend()
	s.cancel()
}

// Arbitrate sends a MasterArbitrationUpdate and waits for the response. It
// returns the status code in the response (OK means this stream is primary).
func (s *Stream) Arbitrate(electionLow uint64, timeout time.Duration) (codes.Code, error) {
	if err := s.stream.Send(&p4pb.StreamMessageRequest{Update: &p4pb.StreamMessageRequest_Arbitration{
		Arbitration: &p4pb.MasterArbitrationUpdate{DeviceId: s.DeviceID, ElectionId: &p4pb.Uint128{Low: electionLow}},
	}}); err != nil {
		return codes.Unknown, fmt.Errorf("send arbitration: %w", err)
	}
	select {
	case a := <-s.Arb:
		return codes.Code(a.GetStatus().GetCode()), nil
	case <-s.done:
		return codes.Unknown, fmt.Errorf("stream terminated: %v", s.Err())
	case <-time.After(timeout):
		return codes.Unknown, fmt.Errorf("no arbitration response within %v", timeout)
	}
}

// SendPacket sends a PacketOut on the stream.
func (s *Stream) SendPacket(p *p4pb.PacketOut) error {
	return s.stream.Send(&p4pb.StreamMessageRequest{Update: &p4pb.StreamMessageRequest_Packet{Packet: p}})
}

// Alive reports whether the stream is still open.
func (s *Stream) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}
