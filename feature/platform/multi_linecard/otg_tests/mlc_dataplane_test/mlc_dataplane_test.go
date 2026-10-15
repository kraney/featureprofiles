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

// Package mlc_dataplane_test checks forwarding between linecards of a modular
// DUT: plain cross-linecard forwarding, gRIBI next-hop groups that span
// linecards, and a LAG whose members sit on different linecards. Subtest names
// are the requirement IDs in feature/platform/multi_linecard/README.md.
package mlc_dataplane_test

import (
	"flag"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/open-traffic-generator/snappi/gosnappi"
	"github.com/openconfig/featureprofiles/internal/attrs"
	"github.com/openconfig/featureprofiles/internal/deviations"
	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/featureprofiles/internal/gribi"
	"github.com/openconfig/featureprofiles/internal/mlc"
	"github.com/openconfig/featureprofiles/internal/otgutils"
	"github.com/openconfig/gribigo/client"
	"github.com/openconfig/gribigo/constants"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"
	"github.com/openconfig/ondatra/netutil"
	"github.com/openconfig/ygot/ygot"
)

const (
	plen        = 30
	gribiPfx    = "198.51.100.0/24"
	gribiDstMin = "198.51.100.1"
	nh1ID       = 101
	nh2ID       = 102
	nhgID       = 100
	pps         = 100
	trafficTime = 10 * time.Second
	lossTol     = 0.0
)

func TestMain(m *testing.M) {
	fptest.RunTests(m)
}

var lagATEPorts = flag.String("mlc_lag_ate_ports", "eth2,eth3", "Comma-separated ATE port names preferred as LAG members (KNE ixia-c requires LAG ports to be grouped in the topology).")

// portAttrs returns DUT and ATE attributes for testbed port index i (0-based).
func portAttrs(i int) (dut, ate attrs.Attributes) {
	base := 4 * i
	dut = attrs.Attributes{Desc: fmt.Sprintf("dutPort%d", i+1), IPv4: fmt.Sprintf("192.0.2.%d", base+1), IPv4Len: plen}
	ate = attrs.Attributes{
		Name: fmt.Sprintf("atePort%d", i+1), MAC: fmt.Sprintf("02:00:01:01:01:%02x", i+1),
		IPv4: fmt.Sprintf("192.0.2.%d", base+2), IPv4Len: plen,
	}
	return dut, ate
}

type fixture struct {
	dut      *ondatra.DUTDevice
	ate      *ondatra.ATEDevice
	ports    []*ondatra.Port // DUT ports ordered port1..portN
	atePorts []*ondatra.Port
	slot     map[string]int
	top      gosnappi.Config
	dutA     []attrs.Attributes
	ateA     []attrs.Attributes
}

func newFixture(t *testing.T) *fixture {
	dut, ate := ondatra.DUT(t, "dut"), ondatra.ATE(t, "ate")
	f := &fixture{dut: dut, ate: ate, slot: mlc.DUTPortSlots(t, dut)}
	for i := 1; i <= 4; i++ {
		f.ports = append(f.ports, dut.Port(t, fmt.Sprintf("port%d", i)))
		f.atePorts = append(f.atePorts, ate.Port(t, fmt.Sprintf("port%d", i)))
		d, a := portAttrs(i - 1)
		f.dutA, f.ateA = append(f.dutA, d), append(f.ateA, a)
	}
	mlc.EnablePorts(t, dut)
	f.configureDUT(t)
	f.configureATE(t)
	return f
}

func (f *fixture) configureDUT(t *testing.T) {
	t.Helper()
	fptest.ConfigureDefaultNetworkInstance(t, f.dut)
	for i, p := range f.ports {
		gnmi.Replace(t, f.dut, gnmi.OC().Interface(p.Name()).Config(), f.dutA[i].NewOCInterface(p.Name(), f.dut))
		if deviations.ExplicitInterfaceInDefaultVRF(f.dut) {
			fptest.AssignToNetworkInstance(t, f.dut, p.Name(), deviations.DefaultNetworkInstance(f.dut), 0)
		}
	}
}

func (f *fixture) configureATE(t *testing.T) {
	t.Helper()
	f.top = gosnappi.NewConfig()
	for i, p := range f.atePorts {
		f.ateA[i].AddToOTG(f.top, p, &f.dutA[i])
	}
}

// slotPairs returns port indexes (a, b) on different linecards, and (c, d) on
// the same linecard, based on the interface naming convention.
func (f *fixture) crossAndSame(t *testing.T) (cross [][2]int, same [][2]int) {
	t.Helper()
	for i := range f.ports {
		for j := range f.ports {
			if i == j {
				continue
			}
			si, oki := f.slot[f.ports[i].Name()]
			sj, okj := f.slot[f.ports[j].Name()]
			if !oki || !okj {
				continue
			}
			if si != sj {
				cross = append(cross, [2]int{i, j})
			} else {
				same = append(same, [2]int{i, j})
			}
		}
	}
	return cross, same
}

func (f *fixture) addFlow(name string, src int, dsts []int, dstIP string, count int) {
	fl := f.top.Flows().Add().SetName(name)
	fl.Metrics().SetEnable(true)
	var rx []string
	for _, d := range dsts {
		rx = append(rx, f.ateA[d].Name+".IPv4")
	}
	fl.TxRx().Device().SetTxNames([]string{f.ateA[src].Name + ".IPv4"}).SetRxNames(rx)
	fl.Size().SetFixed(512)
	fl.Rate().SetPps(pps)
	eth := fl.Packet().Add().Ethernet()
	eth.Src().SetValue(f.ateA[src].MAC)
	ip := fl.Packet().Add().Ipv4()
	ip.Src().SetValue(f.ateA[src].IPv4)
	if count > 1 {
		ip.Dst().Increment().SetStart(dstIP).SetCount(uint32(count))
	} else {
		ip.Dst().SetValue(dstIP)
	}
}

func (f *fixture) runFlows(t *testing.T, during func()) {
	t.Helper()
	f.ate.OTG().PushConfig(t, f.top)
	f.ate.OTG().StartProtocols(t)
	otgutils.WaitForARP(t, f.ate.OTG(), f.top, "IPv4")
	f.ate.OTG().StartTraffic(t)
	if during != nil {
		during()
	}
	time.Sleep(trafficTime)
	f.ate.OTG().StopTraffic(t)
	time.Sleep(2 * time.Second)
	otgutils.LogFlowMetrics(t, f.ate.OTG(), f.top)
}

func (f *fixture) checkNoLoss(t *testing.T, flows []string) {
	t.Helper()
	for _, fl := range flows {
		if loss := otgutils.GetFlowLossPct(t, f.ate.OTG(), fl, 10*time.Second); loss > lossTol {
			t.Errorf("flow %s: %.2f%% loss, want 0", fl, loss)
		}
	}
}

func counters(t *testing.T, dut *ondatra.DUTDevice, ports []*ondatra.Port) map[string][2]uint64 {
	t.Helper()
	out := map[string][2]uint64{}
	for _, p := range ports {
		c := gnmi.OC().Interface(p.Name()).Counters()
		in, _ := gnmi.Lookup(t, dut, c.InPkts().State()).Val()
		o, _ := gnmi.Lookup(t, dut, c.OutPkts().State()).Val()
		out[p.Name()] = [2]uint64{in, o}
	}
	return out
}

func TestMLCDataplane(t *testing.T) {
	f := newFixture(t)
	cross, same := f.crossAndSame(t)
	if len(cross) == 0 {
		t.Fatalf("testbed ports %v do not span two linecards", f.slot)
	}

	t.Run("MLC-DP-001_CrossLinecardForwarding", func(t *testing.T) {
		f.configureATE(t)
		var flows []string
		// One flow per ordered pair of ports on different linecards, plus one
		// between the two ICs of the same linecard.
		pairs := cross
		if len(same) > 0 {
			pairs = append(pairs, same[0])
		}
		for _, pr := range pairs {
			name := fmt.Sprintf("p%d_to_p%d", pr[0]+1, pr[1]+1)
			f.addFlow(name, pr[0], []int{pr[1]}, f.ateA[pr[1]].IPv4, 1)
			flows = append(flows, name)
		}
		before := counters(t, f.dut, f.ports)
		f.runFlows(t, nil)
		f.checkNoLoss(t, flows)
		after := counters(t, f.dut, f.ports)

		t.Run("MLC-INV-007_CountersOnAllLinecards", func(t *testing.T) {
			for _, p := range f.ports {
				b, a := before[p.Name()], after[p.Name()]
				if a[0] <= b[0] {
					t.Errorf("%s (slot %d): in-pkts did not increase (%d -> %d)", p.Name(), f.slot[p.Name()], b[0], a[0])
				}
				if a[1] <= b[1] {
					t.Errorf("%s (slot %d): out-pkts did not increase (%d -> %d)", p.Name(), f.slot[p.Name()], b[1], a[1])
				}
			}
		})
	})

	// Pick two egress ports on different linecards and an ingress port.
	in, eg1, eg2 := cross[0][0], -1, -1
	for _, pr := range cross {
		if pr[0] == in {
			eg2 = pr[1]
			break
		}
	}
	for _, pr := range same {
		if pr[0] == in {
			eg1 = pr[1]
			break
		}
	}
	if eg1 < 0 {
		for i := range f.ports {
			if i != in && i != eg2 {
				eg1 = i
				break
			}
		}
	}

	ni := deviations.DefaultNetworkInstance(f.dut)
	gc := &gribi.Client{DUT: f.dut, FIBACK: true, Persistence: true}
	gribiOK := t.Run("MLC-GRIBI-001_NHGSpanningLinecards", func(t *testing.T) {
		if err := gc.Start(t); err != nil {
			t.Fatalf("gRIBI Start: %v", err)
		}
		gc.BecomeLeader(t)
		gc.FlushAll(t)
		// Send the whole chain in one batch: some servers only acknowledge a next
		// hop as FIB_PROGRAMMED once a route references it.
		nh1, nh1Res := gribi.NHEntry(nh1ID, f.ateA[eg1].IPv4, ni, fluent.InstalledInFIB)
		nh2, nh2Res := gribi.NHEntry(nh2ID, f.ateA[eg2].IPv4, ni, fluent.InstalledInFIB)
		nhg, nhgRes := gribi.NHGEntry(nhgID, map[uint64]uint64{nh1ID: 1, nh2ID: 1}, ni, fluent.InstalledInFIB)
		route := fluent.IPv4Entry().WithNetworkInstance(ni).WithPrefix(gribiPfx).WithNextHopGroup(nhgID)
		routeRes := fluent.OperationResult().WithIPv4Operation(gribiPfx).WithOperationType(constants.Add).
			WithProgrammingResult(fluent.InstalledInFIB).AsResult()
		gc.AddEntries(t, []fluent.GRIBIEntry{nh1, nh2, nhg, route}, []*client.OpResult{nh1Res, nh2Res, nhgRes, routeRes})

		f.configureATE(t)
		f.addFlow("gribi_nhg", in, []int{eg1, eg2}, gribiDstMin, 200)
		before := counters(t, f.dut, f.ports)
		f.runFlows(t, nil)
		f.checkNoLoss(t, []string{"gribi_nhg"})
		after := counters(t, f.dut, f.ports)
		for _, e := range []int{eg1, eg2} {
			p := f.ports[e]
			if after[p.Name()][1]-before[p.Name()][1] < 100 {
				t.Errorf("%s (slot %d): next hop egress carried %d packets; want traffic on both linecards", p.Name(), f.slot[p.Name()], after[p.Name()][1]-before[p.Name()][1])
			}
		}
	})

	t.Run("MLC-GRIBI-002_EntriesSurviveReconnect", func(t *testing.T) {
		if !gribiOK {
			gc.Close(t)
			t.Skip("MLC-GRIBI-001 did not program the next-hop group; nothing to preserve")
		}
		f.configureATE(t)
		f.addFlow("gribi_reconnect", in, []int{eg1, eg2}, gribiDstMin, 200)
		f.runFlows(t, func() {
			gc.Close(t)
			gc = &gribi.Client{DUT: f.dut, FIBACK: true, Persistence: true}
			if err := gc.Start(t); err != nil {
				t.Errorf("gRIBI restart: %v", err)
				return
			}
			gc.BecomeLeader(t)
		})
		t.Run("MLC-DP-003_ForwardingDuringSessionChurn", func(t *testing.T) {
			f.checkNoLoss(t, []string{"gribi_reconnect"})
		})
		res, err := gc.Fluent(t).Get().WithNetworkInstance(ni).WithAFT(fluent.IPv4).Send()
		if err != nil {
			t.Fatalf("gRIBI Get: %v", err)
		}
		found := false
		for _, e := range res.GetEntry() {
			if e.GetIpv4().GetPrefix() == gribiPfx {
				found = true
			}
		}
		if !found {
			t.Errorf("%s not present after gRIBI reconnect with a higher election ID", gribiPfx)
		}
		gc.FlushAll(t)
		gc.Close(t)
	})

	t.Run("MLC-INV-008_LAGAcrossLinecards", func(t *testing.T) {
		// Pick the members first (emulated ATEs may require specific ports),
		// then any other port as the ingress.
		members := f.lagMembers(t, -1)
		if len(members) != 2 {
			t.Fatal("could not find two LAG member ports on different linecards")
		}
		in := -1
		for i := range f.ports {
			if i != members[0] && i != members[1] {
				in = i
				break
			}
		}
		aggID := netutil.NextAggregateInterface(t, f.dut)
		lagDUT := attrs.Attributes{Desc: "mlcLag", IPv4: "192.0.2.65", IPv4Len: plen}
		lagATE := attrs.Attributes{Name: "ateLag", MAC: "02:00:01:01:02:01", IPv4: "192.0.2.66", IPv4Len: plen}
		f.configureLAG(t, aggID, members, lagDUT)

		// ATE: ingress device on port `in`, static LAG on the member ports.
		f.top = gosnappi.NewConfig()
		f.ateA[in].AddToOTG(f.top, f.atePorts[in], &f.dutA[in])
		lag := f.top.Lags().Add().SetName("ateLag")
		lag.Protocol().Static().SetLagId(1)
		for i, m := range members {
			f.top.Ports().Add().SetName(f.atePorts[m].ID())
			lag.Ports().Add().SetPortName(f.atePorts[m].ID()).Ethernet().SetMac(fmt.Sprintf("02:00:01:01:02:%02x", i+2)).SetName(fmt.Sprintf("ateLag.m%d", i))
		}
		dev := f.top.Devices().Add().SetName("ateLagDev")
		eth := dev.Ethernets().Add().SetName("ateLag.Eth").SetMac(lagATE.MAC)
		eth.Connection().SetLagName("ateLag")
		eth.Ipv4Addresses().Add().SetName("ateLag.IPv4").SetAddress(lagATE.IPv4).SetGateway(lagDUT.IPv4).SetPrefix(plen)

		gnmi.Await(t, f.dut, gnmi.OC().Interface(aggID).OperStatus().State(), 2*time.Minute, oc.Interface_OperStatus_UP)
		var want []string
		for _, m := range members {
			want = append(want, f.ports[m].Name())
			if got, _ := gnmi.Lookup(t, f.dut, gnmi.OC().Interface(f.ports[m].Name()).Ethernet().AggregateId().State()).Val(); got != aggID {
				t.Errorf("%s: ethernet/state/aggregate-id = %q, want %q", f.ports[m].Name(), got, aggID)
			}
		}
		got, _ := gnmi.Lookup(t, f.dut, gnmi.OC().Interface(aggID).Aggregation().Member().State()).Val()
		sort.Strings(got)
		sort.Strings(want)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s aggregation/state/member = %v, want %v", aggID, got, want)
		}

		fl := f.top.Flows().Add().SetName("to_lag")
		fl.Metrics().SetEnable(true)
		fl.TxRx().Device().SetTxNames([]string{f.ateA[in].Name + ".IPv4"}).SetRxNames([]string{"ateLag.IPv4"})
		fl.Size().SetFixed(512)
		fl.Rate().SetPps(pps)
		fl.Packet().Add().Ethernet().Src().SetValue(f.ateA[in].MAC)
		ip := fl.Packet().Add().Ipv4()
		ip.Src().SetValue(f.ateA[in].IPv4)
		ip.Dst().SetValue(lagATE.IPv4)
		udp := fl.Packet().Add().Udp()
		udp.SrcPort().Increment().SetStart(1024).SetCount(1000)
		udp.DstPort().SetValue(4000)
		before := counters(t, f.dut, f.ports)
		f.runFlows(t, nil)
		f.checkNoLoss(t, []string{"to_lag"})
		after := counters(t, f.dut, f.ports)
		for _, m := range members {
			p := f.ports[m]
			if after[p.Name()][1] <= before[p.Name()][1] {
				t.Errorf("LAG member %s (slot %d) carried no traffic", p.Name(), f.slot[p.Name()])
			}
		}
	})
}

// lagMembers returns two DUT port indexes on different linecards, excluding
// the ingress port. ATE ports named in -mlc_lag_ate_ports are preferred, since
// emulated ATEs may only support LAGs on pre-grouped ports.
func (f *fixture) lagMembers(t *testing.T, ingress int) []int {
	t.Helper()
	preferred := map[string]bool{}
	for _, n := range strings.Split(*lagATEPorts, ",") {
		if n != "" {
			preferred[n] = true
		}
	}
	pick := func(onlyPreferred bool) []int {
		for i := range f.ports {
			for j := i + 1; j < len(f.ports); j++ {
				if i == ingress || j == ingress {
					continue
				}
				if f.slot[f.ports[i].Name()] == f.slot[f.ports[j].Name()] {
					continue
				}
				if onlyPreferred && !(preferred[f.atePorts[i].Name()] && preferred[f.atePorts[j].Name()]) {
					continue
				}
				return []int{i, j}
			}
		}
		return nil
	}
	if m := pick(true); m != nil {
		return m
	}
	return pick(false)
}

func (f *fixture) configureLAG(t *testing.T, aggID string, members []int, a attrs.Attributes) {
	t.Helper()
	if deviations.ExplicitInterfaceInDefaultVRF(f.dut) {
		// Members can no longer carry their own routed subinterface.
		ni := deviations.DefaultNetworkInstance(f.dut)
		for _, m := range members {
			gnmi.Delete(t, f.dut, gnmi.OC().NetworkInstance(ni).Interface(f.ports[m].Name()+".0").Config())
		}
	}
	if deviations.AggregateAtomicUpdate(f.dut) {
		d := &oc.Root{}
		agg := d.GetOrCreateInterface(aggID)
		agg.Type = oc.IETFInterfaces_InterfaceType_ieee8023adLag
		agg.GetOrCreateAggregation().LagType = oc.IfAggregate_AggregationType_STATIC
		for _, m := range members {
			gnmi.Delete(t, f.dut, gnmi.OC().Interface(f.ports[m].Name()).Subinterface(0).Config())
			i := d.GetOrCreateInterface(f.ports[m].Name())
			i.GetOrCreateEthernet().AggregateId = ygot.String(aggID)
			i.Type = oc.IETFInterfaces_InterfaceType_ethernetCsmacd
			if deviations.InterfaceEnabled(f.dut) {
				i.Enabled = ygot.Bool(true)
			}
		}
		gnmi.Update(t, f.dut, gnmi.OC().Config(), d)
	}
	agg := a.NewOCInterface(aggID, f.dut)
	agg.Type = oc.IETFInterfaces_InterfaceType_ieee8023adLag
	agg.GetOrCreateAggregation().LagType = oc.IfAggregate_AggregationType_STATIC
	gnmi.Replace(t, f.dut, gnmi.OC().Interface(aggID).Config(), agg)
	if deviations.ExplicitInterfaceInDefaultVRF(f.dut) {
		fptest.AssignToNetworkInstance(t, f.dut, aggID, deviations.DefaultNetworkInstance(f.dut), 0)
	}
	for _, m := range members {
		i := &oc.Interface{Name: ygot.String(f.ports[m].Name()), Type: oc.IETFInterfaces_InterfaceType_ethernetCsmacd}
		i.GetOrCreateEthernet().AggregateId = ygot.String(aggID)
		if deviations.InterfaceEnabled(f.dut) {
			i.Enabled = ygot.Bool(true)
		}
		gnmi.Replace(t, f.dut, gnmi.OC().Interface(f.ports[m].Name()).Config(), i)
	}
}
