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

// Package mlc_p4rt_test checks that every forwarding chip of a modular DUT can
// be programmed as an independent P4Runtime device and that packet I/O is
// attributed to the chip that owns the port. Subtest names are the requirement
// IDs in feature/platform/multi_linecard/README.md.
package mlc_p4rt_test

import (
	"context"
	"flag"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cisco-open/go-p4/utils"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/open-traffic-generator/snappi/gosnappi"
	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/featureprofiles/internal/mlc"
	"github.com/openconfig/featureprofiles/internal/p4rtutils"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"
	"google.golang.org/grpc/codes"

	p4pb "github.com/p4lang/p4runtime/go/p4/v1"
)

var (
	p4InfoFile = flag.String("p4info_file_location", "../../../../p4rt/data/wbb.p4info.pb.txt", "Path to the p4info file.")
)

const (
	lldpEtherType       = 0x88cc
	lldpDstMAC          = "01:80:c2:00:00:0e"
	metadataIngressPort = 1
	arbTimeout          = 10 * time.Second
	registerTimeout     = 90 * time.Second
	puntTimeout         = 20 * time.Second
)

// Election IDs increase across runs because some servers remember the highest
// election ID they have seen and treat lower IDs as backups.
var (
	firstElection  = uint64(time.Now().Unix()) * 2
	secondElection = firstElection + 1
)

func TestMain(m *testing.M) {
	fptest.RunTests(m)
}

type fixture struct {
	dut     *ondatra.DUTDevice
	ate     *ondatra.ATEDevice
	inv     *mlc.Inventory
	nodeIDs map[string]uint64 // IC name -> node-id
	portIDs map[string]uint32 // DUT interface -> interface ID
	client  p4pb.P4RuntimeClient
	streams map[string]*mlc.Stream // IC name -> primary stream
	// lastIngressPorts maps a DUT port's LLDP source MAC to the ingress_port
	// metadata of the last PacketIn received for it.
	lastIngressPorts map[string]string
}

// srcMAC returns the LLDP source MAC used for the ATE port linked to the DUT
// port with the same testbed ID (port<N>), so a PacketIn identifies its port.
func srcMAC(portID string) string {
	n, _ := strconv.Atoi(strings.TrimPrefix(portID, "port"))
	return fmt.Sprintf("02:4d:4c:43:00:%02x", n)
}

func newFixture(t *testing.T) *fixture {
	dut := ondatra.DUT(t, "dut")
	f := &fixture{dut: dut, ate: ondatra.ATE(t, "ate"), streams: map[string]*mlc.Stream{}}
	mlc.EnablePorts(t, dut)
	f.inv = mlc.Discover(t, dut)
	slots := map[int]bool{}
	for _, s := range mlc.DUTPortSlots(t, dut) {
		slots[s] = true
	}
	var slotList []int
	for s := range slots {
		slotList = append(slotList, s)
	}
	sort.Ints(slotList)
	f.nodeIDs = mlc.ConfigureNodeIDs(t, dut, f.inv, mlc.TargetICs(dut, f.inv, slotList))
	f.portIDs = mlc.ConfigurePortIDs(t, dut)
	f.client = dut.RawAPIs().P4RT(t)
	t.Logf("node-ids: %v", f.nodeIDs)
	t.Logf("port IDs: %v", f.portIDs)
	return f
}

func (f *fixture) icNames() []string {
	var out []string
	for ic := range f.nodeIDs {
		out = append(out, ic)
	}
	sort.Strings(out)
	return out
}

func TestMLCP4RT(t *testing.T) {
	f := newFixture(t)
	defer func() {
		for _, s := range f.streams {
			s.Close()
		}
	}()

	t.Run("MLC-P4RT-001_IndependentStreamPerIC", func(t *testing.T) {
		if len(f.nodeIDs) == 0 {
			t.Fatal("no ICs accepted a node-id")
		}
		for _, ic := range f.icNames() {
			// The P4RT server may take a moment to register a newly configured
			// node-id, so retry while it reports the device as not found.
			var (
				s    *mlc.Stream
				code codes.Code
				err  error
			)
			for deadline := time.Now().Add(registerTimeout); ; {
				s, err = mlc.OpenStream(f.client, f.nodeIDs[ic])
				if err == nil {
					code, err = s.Arbitrate(firstElection, arbTimeout)
				}
				if err == nil && code == codes.OK {
					break
				}
				if s != nil {
					s.Close()
				}
				if time.Now().After(deadline) {
					break
				}
				time.Sleep(2 * time.Second)
			}
			if err != nil || code != codes.OK {
				t.Errorf("%s (device_id %d): arbitration = %v, %v; want primary (OK)", ic, f.nodeIDs[ic], code, err)
				continue
			}
			f.streams[ic] = s
		}
		if len(f.streams) < 2 {
			t.Fatalf("only %d ICs became primary; need at least 2 to check independence", len(f.streams))
		}
		// Closing one device's stream must not affect the others.
		names := f.icNames()
		victim := ""
		for _, ic := range names {
			if f.streams[ic] != nil {
				victim = ic
				break
			}
		}
		f.streams[victim].Close()
		delete(f.streams, victim)
		time.Sleep(3 * time.Second)
		for ic, s := range f.streams {
			if !s.Alive() {
				t.Errorf("closing the stream for %s terminated the stream for %s: %v", victim, ic, s.Err())
			}
		}
		s, err := mlc.OpenStream(f.client, f.nodeIDs[victim])
		if err != nil {
			t.Fatalf("%s: reopen StreamChannel: %v", victim, err)
		}
		if code, err := s.Arbitrate(firstElection, arbTimeout); err != nil || code != codes.OK {
			t.Errorf("%s: re-arbitration = %v, %v; want OK", victim, code, err)
		}
		f.streams[victim] = s
	})

	t.Run("MLC-P4RT-002_PerICPipelineAndTables", func(t *testing.T) {
		p4Info, err := utils.P4InfoLoad(p4InfoFile)
		if err != nil {
			t.Fatalf("load p4info %q: %v", *p4InfoFile, err)
		}
		if len(f.streams) == 0 {
			t.Fatal("no primary streams (see MLC-P4RT-001)")
		}
		ctx := context.Background()
		for _, ic := range f.icNames() {
			if f.streams[ic] == nil {
				continue
			}
			dev := f.nodeIDs[ic]
			eid := &p4pb.Uint128{Low: firstElection}
			if _, err := f.client.SetForwardingPipelineConfig(ctx, &p4pb.SetForwardingPipelineConfigRequest{
				DeviceId: dev, ElectionId: eid, Action: p4pb.SetForwardingPipelineConfigRequest_VERIFY_AND_COMMIT,
				Config: &p4pb.ForwardingPipelineConfig{P4Info: p4Info, Cookie: &p4pb.ForwardingPipelineConfig_Cookie{Cookie: dev}},
			}); err != nil {
				t.Errorf("%s: SetForwardingPipelineConfig: %v", ic, err)
				continue
			}
			got, err := f.client.GetForwardingPipelineConfig(ctx, &p4pb.GetForwardingPipelineConfigRequest{
				DeviceId: dev, ResponseType: p4pb.GetForwardingPipelineConfigRequest_COOKIE_ONLY,
			})
			if err != nil {
				t.Errorf("%s: GetForwardingPipelineConfig: %v", ic, err)
			} else if c := got.GetConfig().GetCookie().GetCookie(); c != dev {
				t.Errorf("%s: pipeline cookie = %d, want %d (pipelines are not independent per device)", ic, c, dev)
			}
			if _, err := f.client.Write(ctx, &p4pb.WriteRequest{
				DeviceId: dev, ElectionId: eid, Atomicity: p4pb.WriteRequest_CONTINUE_ON_ERROR,
				Updates: p4rtutils.ACLWbbIngressTableEntryGet([]*p4rtutils.ACLWbbIngressTableEntryInfo{{
					Type: p4pb.Update_INSERT, EtherType: lldpEtherType, EtherTypeMask: 0xFFFF, Priority: 1,
				}}),
			}); err != nil {
				t.Errorf("%s: Write LLDP trap entry: %v", ic, err)
				continue
			}
			rc, err := f.client.Read(ctx, &p4pb.ReadRequest{DeviceId: dev, Entities: []*p4pb.Entity{
				{Entity: &p4pb.Entity_TableEntry{TableEntry: &p4pb.TableEntry{}}},
			}})
			if err != nil {
				t.Errorf("%s: Read: %v", ic, err)
				continue
			}
			n := 0
			for {
				r, err := rc.Recv()
				if err != nil {
					break
				}
				n += len(r.GetEntities())
			}
			if n == 0 {
				t.Errorf("%s: Read returned no table entries after Write", ic)
			}
		}
	})

	t.Run("MLC-DP-002_PacketInOnOwningIC", func(t *testing.T) {
		if len(f.streams) == 0 {
			t.Fatal("no primary streams (see MLC-P4RT-001)")
		}
		seen := f.sendLLDPAndCollect(t)
		devToIC := map[uint64]string{}
		for ic, id := range f.nodeIDs {
			devToIC[id] = ic
		}
		punting := map[string]bool{}
		for _, p := range f.dut.Ports() {
			mac := srcMAC(p.ID())
			devs := seen[mac]
			if len(devs) == 0 {
				t.Errorf("%s: no PacketIn for LLDP sent into this port", p.Name())
				continue
			}
			if len(devs) > 1 {
				t.Errorf("%s: PacketIn delivered on %d device streams %v, want exactly 1", p.Name(), len(devs), devs)
			}
			want, err := f.inv.OwningIC(p.Name())
			for dev := range devs {
				punting[devToIC[dev]] = true
				if err == nil && devToIC[dev] != want {
					t.Errorf("%s: PacketIn arrived on %s, but the port is owned by %s", p.Name(), devToIC[dev], want)
				}
			}
		}
		if want := len(f.dut.Ports()); len(punting) < want {
			t.Errorf("PacketIns for %d ports (one per IC) arrived on only %d distinct devices %v", want, len(punting), keys(punting))
		}
	})

	t.Run("MLC-P4RT-003_SlotEncodedPortIDs", func(t *testing.T) {
		for p, id := range f.portIDs {
			got, ok := gnmi.Lookup(t, f.dut, gnmi.OC().Interface(p).Id().State()).Val()
			if !ok || got != id {
				t.Errorf("%s: state/id = %v (present=%v), want %d", p, got, ok, id)
			}
		}
		if len(f.streams) == 0 {
			t.Skip("no primary streams; cannot check PacketIn metadata")
		}
		got := f.lastIngressPorts
		if got == nil {
			got = f.sendLLDPAndIngress(t)
		}
		for _, p := range f.dut.Ports() {
			want := strconv.Itoa(int(f.portIDs[p.Name()]))
			if v, ok := got[srcMAC(p.ID())]; !ok {
				t.Errorf("%s: no PacketIn to check ingress_port", p.Name())
			} else if v != want {
				t.Errorf("%s: PacketIn ingress_port = %q, want %q", p.Name(), v, want)
			}
		}
	})

	t.Run("MLC-P4RT-004_RearbitrationIsHitless", func(t *testing.T) {
		if len(f.streams) == 0 {
			t.Fatal("no primary streams (see MLC-P4RT-001)")
		}
		before := mlc.Discover(t, f.dut)
		wasUp := map[string]bool{}
		for _, p := range f.dut.Ports() {
			wasUp[p.Name()] = gnmi.Get(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State()) == oc.Interface_OperStatus_UP
		}
		newStreams := map[string]*mlc.Stream{}
		for ic, old := range f.streams {
			s, err := mlc.OpenStream(f.client, f.nodeIDs[ic])
			if err != nil {
				t.Errorf("%s: StreamChannel: %v", ic, err)
				continue
			}
			if code, err := s.Arbitrate(secondElection, arbTimeout); err != nil || code != codes.OK {
				t.Errorf("%s: new primary with higher election_id: %v, %v; want OK", ic, code, err)
			}
			newStreams[ic] = s
			select {
			case a := <-old.Arb:
				if codes.Code(a.GetStatus().GetCode()) == codes.OK {
					t.Errorf("%s: old primary was told it is still primary", ic)
				}
			case <-time.After(arbTimeout):
				t.Errorf("%s: old primary was not notified of the new primary", ic)
			}
		}
		for ic, s := range f.streams {
			s.Close()
			if n, ok := newStreams[ic]; ok {
				f.streams[ic] = n
			} else {
				delete(f.streams, ic)
			}
		}
		after := mlc.Discover(t, f.dut)
		for ic, c := range before.Components {
			a := after.Components[ic]
			if a == nil || a.OperStatus != c.OperStatus || a.NodeID != c.NodeID {
				t.Errorf("%s changed across controller re-arbitration: before %+v, after %+v", ic, c, a)
			}
		}
		for _, p := range f.dut.Ports() {
			if !wasUp[p.Name()] {
				continue
			}
			if st := gnmi.Get(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State()); st != oc.Interface_OperStatus_UP {
				t.Errorf("%s oper-status = %v after re-arbitration, want UP", p.Name(), st)
			}
		}
	})
}

// sendLLDPAndCollect sends LLDP frames from every ATE port and returns, for
// each source MAC, the set of device_ids whose stream delivered a PacketIn.
// It also records each source MAC's ingress_port metadata.
func (f *fixture) sendLLDPAndCollect(t *testing.T) map[string]map[uint64]bool {
	t.Helper()
	cfg := gosnappi.NewConfig()
	for _, p := range f.ate.Ports() {
		cfg.Ports().Add().SetName(p.ID())
		fl := cfg.Flows().Add().SetName("lldp_" + p.ID())
		fl.Metrics().SetEnable(true)
		fl.TxRx().Port().SetTxName(p.ID())
		eth := fl.Packet().Add().Ethernet()
		eth.Src().SetValue(srcMAC(p.ID()))
		eth.Dst().SetValue(lldpDstMAC)
		eth.EtherType().SetValue(lldpEtherType)
		fl.Size().SetFixed(128)
		fl.Rate().SetPps(5)
		fl.Duration().FixedPackets().SetPackets(10)
	}
	f.ate.OTG().PushConfig(t, cfg)
	f.ate.OTG().StartTraffic(t)
	seen := map[string]map[uint64]bool{}
	f.lastIngressPorts = map[string]string{}
	deadline := time.After(puntTimeout)
	for done := false; !done; {
		select {
		case <-deadline:
			done = true
		default:
			for ic, s := range f.streams {
				select {
				case pkt := <-s.Packets:
					src := packetSrc(pkt.GetPayload())
					if seen[src] == nil {
						seen[src] = map[uint64]bool{}
					}
					seen[src][f.nodeIDs[ic]] = true
					for _, m := range pkt.GetMetadata() {
						if m.GetMetadataId() == metadataIngressPort {
							f.lastIngressPorts[src] = string(m.GetValue())
						}
					}
				default:
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	f.ate.OTG().StopTraffic(t)
	f.logPuntDiagnostics(t)
	return seen
}

func (f *fixture) sendLLDPAndIngress(t *testing.T) map[string]string {
	f.sendLLDPAndCollect(t)
	return f.lastIngressPorts
}

// logPuntDiagnostics logs ATE transmit and DUT receive counters, to tell a
// missing PacketIn apart from frames that never reached the DUT.
func (f *fixture) logPuntDiagnostics(t *testing.T) {
	t.Helper()
	for _, p := range f.ate.Ports() {
		fm := gnmi.Lookup(t, f.ate.OTG(), gnmi.OTG().Flow("lldp_"+p.ID()).Counters().OutPkts().State())
		tx, _ := fm.Val()
		t.Logf("ATE flow lldp_%s: tx=%d", p.ID(), tx)
	}
	for _, p := range f.dut.Ports() {
		c := gnmi.OC().Interface(p.Name()).Counters()
		in, _ := gnmi.Lookup(t, f.dut, c.InPkts().State()).Val()
		mc, _ := gnmi.Lookup(t, f.dut, c.InMulticastPkts().State()).Val()
		t.Logf("DUT %s: in-pkts=%d in-multicast-pkts=%d", p.Name(), in, mc)
	}
}

func packetSrc(b []byte) string {
	pkt := gopacket.NewPacket(b, layers.LayerTypeEthernet, gopacket.Default)
	if e, ok := pkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet); ok {
		return net.HardwareAddr(e.SrcMAC).String()
	}
	return ""
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
