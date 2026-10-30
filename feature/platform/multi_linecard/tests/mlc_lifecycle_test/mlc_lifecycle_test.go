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

// Package mlc_lifecycle_test checks that a modular DUT reports linecard
// lifecycle events per linecard and recovers every linecard and chip after a
// chassis reboot. Subtest names are the requirement IDs in
// feature/platform/multi_linecard/README.md.
package mlc_lifecycle_test

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/openconfig/featureprofiles/internal/components"
	"github.com/openconfig/featureprofiles/internal/deviations"
	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/featureprofiles/internal/mlc"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"
	"github.com/openconfig/ygnmi/ygnmi"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc/codes"

	spb "github.com/openconfig/gnoi/system"
	tpb "github.com/openconfig/gnoi/types"
)

const (
	linecardTimeout = 15 * time.Minute
	rebootDownWait  = 5 * time.Minute
	rebootUpWait    = 30 * time.Minute
	linkTimeout     = 10 * time.Minute
	arbTimeout      = 10 * time.Second
	registerTimeout = 5 * time.Minute
)

func TestMain(m *testing.M) {
	fptest.RunTests(m)
}

type fixture struct {
	dut       *ondatra.DUTDevice
	inv       *mlc.Inventory
	portSlot  map[string]int
	slots     []int
	lcBySlot  map[int]string
	nodeIDs   map[string]uint64
	naming    mlc.Naming
	hasNaming bool
}

func newFixture(t *testing.T) *fixture {
	dut := ondatra.DUT(t, "dut")
	f := &fixture{dut: dut, portSlot: mlc.DUTPortSlots(t, dut), lcBySlot: map[int]string{}}
	f.naming, f.hasNaming = mlc.NamingFor(dut)
	seen := map[int]bool{}
	for _, s := range f.portSlot {
		if !seen[s] {
			seen[s] = true
			f.slots = append(f.slots, s)
		}
	}
	sort.Ints(f.slots)
	f.refresh(t)
	// Heal any linecard in slots 0..1 or f.slots left non-ACTIVE by a prior
	// interrupted run before enabling ports and awaiting link UP.
	healed := false
	healSlots := append([]int{0, 1}, f.slots...)
	seenHeal := map[string]bool{}
	for _, s := range healSlots {
		lc, ok := f.lcBySlot[s]
		if !ok && f.hasNaming {
			lc = f.naming.LinecardName(s)
		}
		if lc == "" || seenHeal[lc] {
			continue
		}
		seenHeal[lc] = true
		if comp := f.inv.Components[lc]; comp == nil || comp.OperStatus != oc.PlatformTypes_COMPONENT_OPER_STATUS_ACTIVE.String() {
			c := gnmi.OC().Component(lc)
			mlc.Try(t, func(testing.TB) {
				if deviations.PowerDisableEnableLeafRefValidation(dut) {
					gnmi.Update(t, dut, c.Config(), &oc.Component{Name: ygot.String(lc)})
				}
				gnmi.Replace(t, dut, c.Linecard().PowerAdminState().Config(), oc.Platform_ComponentPowerType_POWER_DISABLED)
			})
			time.Sleep(5 * time.Second)
			mlc.Try(t, func(testing.TB) {
				if deviations.PowerDisableEnableLeafRefValidation(dut) {
					gnmi.Update(t, dut, c.Config(), &oc.Component{Name: ygot.String(lc)})
				}
				gnmi.Replace(t, dut, c.Linecard().PowerAdminState().Config(), oc.Platform_ComponentPowerType_POWER_ENABLED)
			})
			w := gnmi.Watch(t, dut, c.OperStatus().State(), linecardTimeout, isActive)
			w.Await(t)
			w.Cancel()
			healed = true
		}
	}
	if healed {
		f.refresh(t)
	}
	mlc.EnablePorts(t, dut)
	f.refresh(t)
	f.nodeIDs = mlc.ConfigureNodeIDs(t, dut, f.inv, mlc.TargetICs(dut, f.inv, f.slots))
	for _, p := range dut.Ports() {
		gnmi.Await(t, dut, gnmi.OC().Interface(p.Name()).OperStatus().State(), linkTimeout, oc.Interface_OperStatus_UP)
	}
	return f
}

func (f *fixture) refresh(t testing.TB) {
	f.inv = mlc.Discover(t, f.dut)
	f.lcBySlot = map[int]string{}
	for _, lc := range f.inv.Linecards() {
		if id := f.inv.Components[lc].SlotID; id != "" {
			if n, err := strconv.Atoi(id); err == nil {
				f.lcBySlot[n] = lc
				continue
			}
			if f.hasNaming {
				if n, ok := mlc.SlotFromName(f.naming.Linecard, id); ok {
					f.lcBySlot[n] = lc
					continue
				}
			}
		}
		if f.hasNaming {
			if n, ok := mlc.SlotFromName(f.naming.Linecard, lc); ok {
				f.lcBySlot[n] = lc
			}
		}
	}
}

// victim returns the linecard holding the last DUT port, plus the ports on that
// linecard and on the other linecards.
func (f *fixture) victim(t *testing.T) (string, []*ondatra.Port, []*ondatra.Port) {
	t.Helper()
	if len(f.slots) < 2 {
		t.Fatalf("DUT ports span slots %v; need at least two linecards", f.slots)
	}
	s := f.slots[len(f.slots)-1]
	lc, ok := f.lcBySlot[s]
	if !ok {
		t.Fatalf("no LINECARD component for slot %d (see MLC-INV-001)", s)
	}
	var on, off []*ondatra.Port
	for _, p := range f.dut.Ports() {
		if f.portSlot[p.Name()] == s {
			on = append(on, p)
		} else {
			off = append(off, p)
		}
	}
	return lc, on, off
}

func isActive(v *ygnmi.Value[oc.E_PlatformTypes_COMPONENT_OPER_STATUS]) bool {
	s, ok := v.Val()
	return ok && s == oc.PlatformTypes_COMPONENT_OPER_STATUS_ACTIVE
}

// expectLinecardCycle watches lc go non-ACTIVE and back to ACTIVE after trigger
// runs, and checks that ports on other linecards stay UP throughout.
func (f *fixture) expectLinecardCycle(t *testing.T, lc string, on, off []*ondatra.Port, trigger func(t *testing.T), restore func(t *testing.T)) {
	t.Helper()
	restored := false
	if restore != nil {
		defer func() {
			if !restored {
				mlc.Try(t, func(testing.TB) { restore(t) })
			}
		}()
	}
	q := gnmi.OC().Component(lc).OperStatus().State()
	down := gnmi.Watch(t, f.dut, q, linecardTimeout, func(v *ygnmi.Value[oc.E_PlatformTypes_COMPONENT_OPER_STATUS]) bool {
		// A delete of the leaf (no value) counts as the linecard going away.
		return !isActive(v)
	})
	defer down.Cancel()

	// Use a single OCBatch stream for all on/off port oper-status watches so we
	// stay well below per-target concurrent gNMI/gNOI stream limits (e.g. 4 on
	// Juniper EVO).
	portBatch := gnmi.OCBatch()
	for _, p := range on {
		portBatch.AddPaths(gnmi.OC().Interface(p.Name()).OperStatus())
	}
	for _, p := range off {
		portBatch.AddPaths(gnmi.OC().Interface(p.Name()).OperStatus())
	}
	var mu sync.Mutex
	seenUp := map[string]bool{}
	onWentDown := map[string]bool{}
	var badOffPort string
	var badOffStatus oc.E_Interface_OperStatus
	portWatch := gnmi.Watch(t, f.dut, portBatch.State(), linecardTimeout, func(v *ygnmi.Value[*oc.Root]) bool {
		root, ok := v.Val()
		if !ok || root == nil {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		for _, p := range on {
			intf := root.GetInterface(p.Name())
			if intf == nil {
				if seenUp[p.Name()] {
					onWentDown[p.Name()] = true
				}
				continue
			}
			st := intf.GetOperStatus()
			if st == oc.Interface_OperStatus_UP {
				seenUp[p.Name()] = true
			} else if st != oc.Interface_OperStatus_UNSET || seenUp[p.Name()] {
				onWentDown[p.Name()] = true
			}
		}
		for _, p := range off {
			if intf := root.GetInterface(p.Name()); intf != nil {
				if st := intf.GetOperStatus(); st != oc.Interface_OperStatus_UNSET && st != oc.Interface_OperStatus_UP && badOffPort == "" {
					badOffPort = p.Name()
					badOffStatus = st
				}
			}
		}
		return false
	})
	defer portWatch.Cancel()

	trigger(t)
	if _, ok := down.Await(t); !ok {
		t.Errorf("%s: no ON_CHANGE update leaving ACTIVE within %v", lc, linecardTimeout)
	}
	down.Cancel()

	for _, p := range on {
		deadline := time.Now().Add(linkTimeout)
		wentDown := false
		for {
			mu.Lock()
			wentDown = onWentDown[p.Name()]
			mu.Unlock()
			if wentDown {
				break
			}
			var present bool
			var st oc.E_Interface_OperStatus
			if mlc.Try(t, func(t testing.TB) {
				st, present = gnmi.Lookup(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State()).Val()
			}) == nil && (!present || st != oc.Interface_OperStatus_UP) {
				wentDown = true
				break
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !wentDown {
			t.Errorf("%s on %s: no oper-status update leaving UP while the linecard was down", p.Name(), lc)
		}
	}
	if restore != nil {
		restored = true
		restore(t)
	}
	up := gnmi.Watch(t, f.dut, q, linecardTimeout, isActive)
	if _, ok := up.Await(t); !ok {
		t.Errorf("%s: did not return to ACTIVE within %v", lc, linecardTimeout)
	}
	up.Cancel()

	portWatch.Cancel()
	portWatch.Await(t)
	mu.Lock()
	badPort, badStatus := badOffPort, badOffStatus
	mu.Unlock()
	if badPort != "" {
		t.Errorf("%s (other linecard) went %v while %s cycled", badPort, badStatus, lc)
	}
	for _, p := range on {
		gnmi.Await(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State(), linkTimeout, oc.Interface_OperStatus_UP)
	}
	// The chips on the linecard must report their node-id again.
	for ic, id := range f.nodeIDs {
		if parentLC := f.inv.ParentLinecard(ic); parentLC != "" && parentLC != lc {
			continue
		}
		if f.hasNaming {
			if s, ok := mlc.SlotFromName(f.naming.IC, ic); ok && f.lcBySlot[s] != lc {
				continue
			}
		}
		w := gnmi.Watch(t, f.dut, gnmi.OC().Component(ic).IntegratedCircuit().NodeId().State(), linkTimeout, func(v *ygnmi.Value[uint64]) bool {
			got, ok := v.Val()
			return ok && got == id
		})
		if _, ok := w.Await(t); !ok {
			t.Errorf("%s: node-id %d not re-streamed after %s recovered", ic, id, lc)
		}
		w.Cancel()
	}
}

func TestMLCLifecycle(t *testing.T) {
	f := newFixture(t)

	t.Run("MLC-LC-003_LinecardReboot", func(t *testing.T) {
		lc, on, off := f.victim(t)
		f.expectLinecardCycle(t, lc, on, off, func(t *testing.T) {
			_, err := f.dut.RawAPIs().GNOI(t).System().Reboot(context.Background(), &spb.RebootRequest{
				Method:        spb.RebootMethod_COLD,
				Subcomponents: []*tpb.Path{components.GetSubcomponentPath(lc, deviations.GNOISubcomponentPath(f.dut))},
				Message:       "MLC-LC-003 linecard reboot",
			})
			if err != nil {
				t.Fatalf("gNOI System.Reboot of subcomponent %s: %v", lc, err)
			}
		}, nil)
	})

	t.Run("MLC-LC-002_LinecardPowerCycleTelemetry", func(t *testing.T) {
		lc, on, off := f.victim(t)
		c := gnmi.OC().Component(lc)
		pq := c.Linecard().PowerAdminState().Config()
		f.expectLinecardCycle(t, lc, on, off, func(t *testing.T) {
			if deviations.PowerDisableEnableLeafRefValidation(f.dut) {
				gnmi.Update(t, f.dut, c.Config(), &oc.Component{Name: ygot.String(lc)})
			}
			if !fptest.NonFatal(t, func(t testing.TB) { gnmi.Replace(t, f.dut, pq, oc.Platform_ComponentPowerType_POWER_DISABLED) }) {
				t.Fatalf("%s: gNMI Set linecard/config/power-admin-state=POWER_DISABLED rejected", lc)
			}
		}, func(t *testing.T) {
			if deviations.PowerDisableEnableLeafRefValidation(f.dut) {
				gnmi.Update(t, f.dut, c.Config(), &oc.Component{Name: ygot.String(lc)})
			}
			gnmi.Replace(t, f.dut, pq, oc.Platform_ComponentPowerType_POWER_ENABLED)
		})
	})

	t.Run("MLC-LC-001_ChassisColdReboot", func(t *testing.T) {
		before := map[string]bool{}
		for _, lc := range f.inv.Linecards() {
			before[lc] = true
		}
		if _, err := f.dut.RawAPIs().GNOI(t).System().Reboot(context.Background(), &spb.RebootRequest{
			Method:  spb.RebootMethod_COLD,
			Force:   true,
			Message: "MLC-LC-001 chassis reboot",
		}); err != nil {
			t.Fatalf("gNOI System.Reboot(COLD): %v", err)
		}
		if !mlc.WaitForGNMIDown(t, f.dut, rebootDownWait) {
			t.Errorf("DUT stayed reachable for %v after Reboot(COLD)", rebootDownWait)
		}
		if !mlc.WaitForGNMI(t, f.dut, rebootUpWait) {
			t.Fatalf("DUT not reachable over gNMI within %v after reboot", rebootUpWait)
		}
		// Wait for all linecards in f.slots to appear and reach ACTIVE before
		// re-discovering child ICs and pushing node-id configuration.
		lcDeadline := time.Now().Add(linecardTimeout)
		for {
			if mlc.Try(t, func(t testing.TB) { f.refresh(t) }) == nil {
				allFound := true
				for _, s := range f.slots {
					if _, ok := f.lcBySlot[s]; !ok {
						allFound = false
						break
					}
				}
				if allFound {
					break
				}
			}
			if time.Now().After(lcDeadline) {
				break
			}
			time.Sleep(10 * time.Second)
		}
		for _, s := range f.slots {
			lc, ok := f.lcBySlot[s]
			if !ok {
				t.Errorf("slot %d: no LINECARD component after reboot", s)
				continue
			}
			w := gnmi.Watch(t, f.dut, gnmi.OC().Component(lc).OperStatus().State(), linecardTimeout, isActive)
			if _, ok := w.Await(t); !ok {
				t.Errorf("%s: not ACTIVE within %v after reboot", lc, linecardTimeout)
			}
			w.Cancel()
		}
		// Re-apply what a controller would push after a reboot.
		mlc.EnablePorts(t, f.dut)
		f.refresh(t)
		f.nodeIDs = mlc.ConfigureNodeIDs(t, f.dut, f.inv, mlc.TargetICs(f.dut, f.inv, f.slots))

		for ic, id := range f.nodeIDs {
			w := gnmi.Watch(t, f.dut, gnmi.OC().Component(ic).IntegratedCircuit().NodeId().State(), linkTimeout, func(v *ygnmi.Value[uint64]) bool {
				got, ok := v.Val()
				return ok && got == id
			})
			if _, ok := w.Await(t); !ok {
				t.Errorf("%s: node-id %d not streamed after reboot", ic, id)
			}
			w.Cancel()
		}
		client := f.dut.RawAPIs().P4RT(t)
		election := uint64(time.Now().Unix()) * 2
		for ic, id := range f.nodeIDs {
			var code codes.Code
			var err error
			for deadline := time.Now().Add(registerTimeout); ; {
				var s *mlc.Stream
				if s, err = mlc.OpenStream(client, id); err == nil {
					code, err = s.Arbitrate(election, arbTimeout)
					s.Close()
				}
				if (err == nil && code == codes.OK) || time.Now().After(deadline) {
					break
				}
				time.Sleep(5 * time.Second)
			}
			if err != nil || code != codes.OK {
				t.Errorf("%s: P4RT arbitration after reboot = %v, %v; want OK", ic, code, err)
			}
		}
		for _, p := range f.dut.Ports() {
			gnmi.Await(t, f.dut, gnmi.OC().Interface(p.Name()).OperStatus().State(), linkTimeout, oc.Interface_OperStatus_UP)
		}
		t.Run("MLC-KNE-004_SlotPopulationSurvivesRestart", func(t *testing.T) {
			after := map[string]bool{}
			for _, lc := range f.inv.Linecards() {
				after[lc] = true
			}
			for lc := range before {
				if !after[lc] {
					t.Errorf("%s present before reboot but missing after", lc)
				}
			}
			if len(before) == 0 {
				t.Error("no linecards before reboot; nothing to compare")
			}
		})
	})
}
