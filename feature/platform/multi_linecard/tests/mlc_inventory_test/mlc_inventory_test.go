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

// Package mlc_inventory_test checks that a modular DUT exposes its linecards,
// forwarding chips and ports through OpenConfig the way a controller needs in
// order to manage each chip independently. Subtest names are the requirement
// IDs in feature/platform/multi_linecard/README.md.
package mlc_inventory_test

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/featureprofiles/internal/mlc"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"
	"github.com/openconfig/ygnmi/ygnmi"
)

const linkTimeout = 2 * time.Minute

func TestMain(m *testing.M) {
	fptest.RunTests(m)
}

// fixture holds state shared by the subtests.
type fixture struct {
	dut       *ondatra.DUTDevice
	inv       *mlc.Inventory
	naming    mlc.Naming
	hasNaming bool
	// portSlot maps each DUT testbed interface to its linecard slot.
	portSlot map[string]int
	// wantSlots is the sorted set of slots that hold at least one DUT port.
	wantSlots []int
	// lcBySlot maps a slot number to the LINECARD component for that slot.
	lcBySlot map[int]string
}

func newFixture(t *testing.T) *fixture {
	dut := ondatra.DUT(t, "dut")
	f := &fixture{dut: dut, portSlot: mlc.DUTPortSlots(t, dut), lcBySlot: map[int]string{}}
	f.naming, f.hasNaming = mlc.NamingFor(dut)

	seen := map[int]bool{}
	for _, s := range f.portSlot {
		if !seen[s] {
			seen[s] = true
			f.wantSlots = append(f.wantSlots, s)
		}
	}
	sort.Ints(f.wantSlots)

	mlc.EnablePorts(t, dut)
	f.inv = mlc.Discover(t, dut)
	for _, lc := range f.inv.Linecards() {
		if s, ok := f.slotOf(lc); ok {
			f.lcBySlot[s] = lc
		}
	}
	logInventory(t, f)
	return f
}

// slotOf returns a linecard's slot from linecard/state/slot-id, falling back to
// the vendor naming convention.
func (f *fixture) slotOf(lc string) (int, bool) {
	if id := f.inv.Components[lc].SlotID; id != "" {
		if n, err := strconv.Atoi(id); err == nil {
			return n, true
		}
	}
	if f.hasNaming {
		return mlc.SlotFromName(f.naming.Linecard, lc)
	}
	return 0, false
}

// populatedLinecards returns the linecards in slots that hold DUT ports. Other
// slots of the chassis may legitimately be empty.
func (f *fixture) populatedLinecards() []string {
	var out []string
	for _, s := range f.wantSlots {
		if lc, ok := f.lcBySlot[s]; ok {
			out = append(out, lc)
		}
	}
	return out
}

func logInventory(t *testing.T, f *fixture) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "DUT port slots: %v\n", f.portSlot)
	for _, lc := range f.inv.Linecards() {
		c := f.inv.Components[lc]
		fmt.Fprintf(&b, "LINECARD %s slot-id=%q oper-status=%q parent=%q\n", lc, c.SlotID, c.OperStatus, c.Parent)
	}
	for _, ic := range f.inv.ICs() {
		c := f.inv.Components[ic]
		fmt.Fprintf(&b, "INTEGRATED_CIRCUIT %s parent=%q node-id=%v(%v)\n", ic, c.Parent, c.NodeID, c.HasNodeID)
	}
	for _, fab := range f.inv.ByType(oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_FABRIC) {
		fmt.Fprintf(&b, "FABRIC %s oper-status=%q\n", fab, f.inv.Components[fab].OperStatus)
	}
	for _, p := range f.dut.Ports() {
		ic, err := f.inv.OwningIC(p.Name())
		fmt.Fprintf(&b, "port %s (%s) -> IC %q err=%v\n", p.ID(), p.Name(), ic, err)
	}
	t.Log(b.String())
}

func TestMLCInventory(t *testing.T) {
	f := newFixture(t)

	t.Run("MLC-INV-001_LinecardComponents", func(t *testing.T) {
		lcs := f.inv.Linecards()
		if len(lcs) < *mlc.MinLinecards {
			t.Errorf("found %d LINECARD components %v, want at least %d", len(lcs), lcs, *mlc.MinLinecards)
		}
		for _, lc := range lcs {
			if f.inv.Components[lc].SlotID == "" {
				t.Errorf("LINECARD %q has no linecard/state/slot-id", lc)
			}
		}
		for _, s := range f.wantSlots {
			if _, ok := f.lcBySlot[s]; !ok {
				t.Errorf("no LINECARD component for slot %d, which holds DUT ports", s)
			}
		}
	})

	t.Run("MLC-KNE-002_SlotNumbering", func(t *testing.T) {
		if !f.hasNaming {
			t.Skipf("no naming convention known for vendor %v", f.dut.Vendor())
		}
		for _, lc := range f.inv.Linecards() {
			nameSlot, ok := mlc.SlotFromName(f.naming.Linecard, lc)
			if !ok {
				t.Errorf("LINECARD %q does not match naming convention %v", lc, f.naming.Linecard)
				continue
			}
			if id := f.inv.Components[lc].SlotID; id != "" && id != strconv.Itoa(nameSlot) {
				t.Errorf("LINECARD %q has slot-id %q, want %d to match its name", lc, id, nameSlot)
			}
		}
		for intf, s := range f.portSlot {
			if _, ok := f.lcBySlot[s]; !ok {
				t.Errorf("interface %q is in slot %d but no linecard reports that slot", intf, s)
			}
		}
	})

	t.Run("MLC-INV-002_LinecardOperStatusInSnapshot", func(t *testing.T) {
		if len(f.wantSlots) == 0 {
			t.Fatal("no DUT ports with a recognizable slot")
		}
		// Each round is a separate subscription, so the status must be resent
		// in every initial snapshot.
		for round := 1; round <= 2; round++ {
			inv := f.inv
			if round > 1 {
				inv = mlc.Discover(t, f.dut)
			}
			for _, s := range f.wantSlots {
				lc, ok := f.lcBySlot[s]
				if !ok {
					t.Errorf("round %d: slot %d: no LINECARD component", round, s)
					continue
				}
				c, ok := inv.Components[lc]
				if !ok || c.OperStatus == "" {
					t.Errorf("round %d: %s: state/oper-status not sent before sync_response", round, lc)
					continue
				}
				if c.OperStatus != oc.PlatformTypes_COMPONENT_OPER_STATUS_ACTIVE.String() {
					t.Errorf("round %d: %s: oper-status = %s, want ACTIVE", round, lc, c.OperStatus)
				}
			}
		}
	})

	t.Run("MLC-INV-003_ICsPerLinecard", func(t *testing.T) {
		for _, s := range f.wantSlots {
			lc, ok := f.lcBySlot[s]
			if !ok {
				t.Errorf("slot %d: no LINECARD component, so no ICs can be parented to it", s)
				continue
			}
			ics := f.inv.ICsOnLinecard(lc)
			if len(ics) < *mlc.MinICsPerLinecard {
				t.Errorf("%s: found %d INTEGRATED_CIRCUIT children %v, want at least %d", lc, len(ics), ics, *mlc.MinICsPerLinecard)
			}
			if !f.hasNaming {
				continue
			}
			for _, ic := range ics {
				icSlot, ok := mlc.SlotFromName(f.naming.IC, ic)
				if !ok {
					t.Errorf("IC %q does not match naming convention %v", ic, f.naming.IC)
				} else if icSlot != s {
					t.Errorf("IC %q is named for slot %d but parented to %s (slot %d)", ic, icSlot, lc, s)
				}
			}
		}
		for _, ic := range f.inv.ICs() {
			if p := f.inv.Components[ic].Parent; p == "" {
				t.Errorf("IC %q has no state/parent", ic)
			} else if pc, ok := f.inv.Components[p]; !ok || pc.Type != oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_LINECARD.String() {
				t.Errorf("IC %q parent %q is not a LINECARD component", ic, p)
			}
		}
	})

	t.Run("MLC-INV-004_PerICNodeID", func(t *testing.T) {
		// A controller configures node-id on the ICs it expects from its model,
		// whether or not the device advertised them, so configure both the
		// discovered ICs and the conventionally named ICs of each populated slot.
		ics := mlc.TargetICs(f.dut, f.inv, f.wantSlots)
		if len(ics) == 0 {
			t.Fatal("no INTEGRATED_CIRCUIT components to configure")
		}
		want := mlc.ConfigureNodeIDs(t, f.dut, f.inv, ics)
		for _, ic := range keys(boolKeys(want)) {
			id := want[ic]
			w := gnmi.Watch(t, f.dut, gnmi.OC().Component(ic).IntegratedCircuit().NodeId().State(), time.Minute, func(v *ygnmi.Value[uint64]) bool {
				got, ok := v.Val()
				return ok && got == id
			})
			if v, ok := w.Await(t); !ok {
				t.Errorf("%s: state/node-id = %v, want %v", ic, v, id)
			}
		}
		// The node-id must also be part of a fresh initial snapshot.
		inv := mlc.Discover(t, f.dut)
		for ic, id := range want {
			if c := inv.Components[ic]; c == nil || !c.HasNodeID || c.NodeID != id {
				t.Errorf("%s: node-id %v missing from initial snapshot", ic, id)
			}
		}
		t.Logf("configured node-ids: %v", want)
	})

	t.Run("MLC-INV-006_InterfacesOnAllLinecards", func(t *testing.T) {
		if len(f.wantSlots) < *mlc.MinLinecards {
			t.Errorf("testbed DUT ports span slots %v, want at least %d linecards", f.wantSlots, *mlc.MinLinecards)
		}
		for _, p := range f.dut.Ports() {
			if _, ok := f.portSlot[p.Name()]; !ok && f.hasNaming {
				t.Errorf("interface %q does not match the modular naming convention %v", p.Name(), f.naming.Interface)
			}
			gnmi.Await(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State(), linkTimeout, oc.Interface_OperStatus_UP)
			if _, ok := gnmi.Lookup(t, f.dut, gnmi.OC().Interface(p.Name()).Ethernet().PortSpeed().State()).Val(); !ok {
				t.Errorf("interface %q: ethernet/state/port-speed missing", p.Name())
			}
		}
	})

	t.Run("MLC-KNE-003_PortsBoundOnAllLinecards", func(t *testing.T) {
		for _, p := range f.dut.Ports() {
			st := gnmi.Get(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State())
			if st == oc.Interface_OperStatus_NOT_PRESENT {
				t.Errorf("interface %q is NOT_PRESENT; its linecard is not instantiated", p.Name())
			} else if st != oc.Interface_OperStatus_UP {
				t.Errorf("interface %q oper-status = %v, want UP", p.Name(), st)
			}
		}
	})

	t.Run("MLC-INV-012_PortToLinecardOwnership", func(t *testing.T) {
		// A controller scopes linecard events (drain, power-off, reboot) to the
		// ports owned by that linecard's ICs, so each port's owning IC must sit
		// on the port's own linecard. This holds even if a linecard models
		// fewer ICs than the real card (see MLC-INV-005).
		for _, p := range f.dut.Ports() {
			ic, err := f.inv.OwningIC(p.Name())
			if err != nil {
				t.Errorf("%s: %v", p.ID(), err)
				continue
			}
			s, ok := f.portSlot[p.Name()]
			if !ok {
				continue
			}
			want, hasLC := f.lcBySlot[s]
			if lc := f.inv.Components[ic].Parent; !hasLC || want != lc {
				if !hasLC || want == "" {
					want = fmt.Sprintf("the slot-%d linecard (no LINECARD component found for that slot)", s)
				}
				t.Errorf("interface %q (slot %d) is owned by IC %q on %q, want an IC on %s", p.Name(), s, ic, lc, want)
			}
		}
	})

	t.Run("MLC-INV-005_PortToICMapping", func(t *testing.T) {
		icsPerSlot := map[int]map[string]bool{}
		for _, p := range f.dut.Ports() {
			ic, err := f.inv.OwningIC(p.Name())
			if err != nil {
				t.Errorf("%s: %v", p.ID(), err)
				continue
			}
			s := f.portSlot[p.Name()]
			if icsPerSlot[s] == nil {
				icsPerSlot[s] = map[string]bool{}
			}
			icsPerSlot[s][ic] = true
		}
		// The topology places one DUT port on each IC, so the ports on a linecard
		// must not all collapse onto a single IC.
		for s, ics := range icsPerSlot {
			if len(ics) < *mlc.MinICsPerLinecard {
				t.Errorf("DUT ports on slot %d map to %d distinct ICs %v, want %d", s, len(ics), keys(ics), *mlc.MinICsPerLinecard)
			}
		}
		portsByIC := f.inv.PortsByIC()
		for _, lc := range f.populatedLinecards() {
			for _, ic := range f.inv.ICsOnLinecard(lc) {
				if len(portsByIC[ic]) == 0 {
					t.Errorf("IC %q owns no PORT components", ic)
				}
			}
		}
	})

	t.Run("MLC-INV-009_FabricComponents", func(t *testing.T) {
		fabs := f.inv.ByType(oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_FABRIC)
		if len(fabs) == 0 {
			t.Fatal("no FABRIC components found")
		}
		for _, fab := range fabs {
			if f.inv.Components[fab].OperStatus == "" {
				t.Errorf("FABRIC %q has no state/oper-status", fab)
			}
		}
	})

	t.Run("MLC-INV-010_BackplaneCapacityPerIC", func(t *testing.T) {
		// A controller consumes these counters from a STREAM subscription, so
		// accept a value from either the initial snapshot or the stream.
		checked := 0
		for _, lc := range f.populatedLinecards() {
			for _, ic := range f.inv.ICsOnLinecard(lc) {
				checked++
				q := gnmi.OC().Component(ic).IntegratedCircuit().BackplaneFacingCapacity().TotalOperationalCapacity().State()
				if _, ok := gnmi.Watch(t, f.dut, q, 30*time.Second, func(v *ygnmi.Value[uint64]) bool {
					_, present := v.Val()
					return present
				}).Await(t); !ok {
					t.Errorf("IC %q: backplane-facing-capacity/state/total-operational-capacity not streamed within 30s", ic)
				}
			}
		}
		if checked == 0 {
			t.Error("no ICs on populated linecards to check")
		}
	})

	t.Run("MLC-INV-011_UnprefixedEnumValues", func(t *testing.T) {
		lcs := map[string]bool{}
		for _, lc := range f.inv.Linecards() {
			lcs[lc] = true
		}
		if len(lcs) == 0 {
			t.Skip("no LINECARD components to inspect")
		}
		seen := 0
		for _, u := range mlc.RawOnce(t, f.dut, "/components/component[name=*]/state/oper-status", gpb.Encoding_PROTO) {
			name := keyOf(u.GetPath(), "component")
			if !lcs[name] {
				continue
			}
			seen++
			if v := u.GetVal().GetStringVal(); strings.Contains(v, ":") {
				t.Errorf("%s oper-status string_val = %q, want an unprefixed identity (e.g. ACTIVE)", name, v)
			}
		}
		if seen == 0 {
			t.Error("no LINECARD oper-status received in a PROTO-encoded Subscribe")
		}
	})

	t.Run("MLC-LC-004_PortAdminIsolation", func(t *testing.T) {
		ports := f.dut.Ports()
		if len(ports) < 2 {
			t.Skip("need at least two DUT ports")
		}
		// Disable the last port, which is on a different linecard than the first.
		victim := ports[len(ports)-1]
		wasUp := map[string]bool{}
		for _, p := range ports[:len(ports)-1] {
			wasUp[p.Name()] = gnmi.Get(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State()) == oc.Interface_OperStatus_UP
		}
		gnmi.Replace(t, f.dut, gnmi.OC().Interface(victim.Name()).Enabled().Config(), false)
		gnmi.Await(t, f.dut, gnmi.OC().Interface(victim.Name()).OperStatus().State(), linkTimeout, oc.Interface_OperStatus_DOWN)
		for _, p := range ports[:len(ports)-1] {
			if !wasUp[p.Name()] {
				continue
			}
			if st := gnmi.Get(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State()); st != oc.Interface_OperStatus_UP {
				t.Errorf("disabling %q changed %q oper-status to %v", victim.Name(), p.Name(), st)
			}
		}
		gnmi.Replace(t, f.dut, gnmi.OC().Interface(victim.Name()).Enabled().Config(), true)
		gnmi.Await(t, f.dut, gnmi.OC().Interface(victim.Name()).OperStatus().State(), linkTimeout, oc.Interface_OperStatus_UP)
	})
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boolKeys[V any](m map[string]V) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func keyOf(p *gpb.Path, elem string) string {
	for _, e := range p.GetElem() {
		if e.GetName() == elem {
			return e.GetKey()["name"]
		}
	}
	return ""
}
