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

// Package mlc contains helpers shared by the multi-linecard readiness tests in
// feature/platform/multi_linecard. It discovers the linecard, integrated circuit
// and port hierarchy of a modular DUT and knows the per-vendor component naming
// conventions that a controller uses to map chips and ports to linecards.
package mlc

import (
	"flag"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"
	"github.com/openconfig/ygnmi/ygnmi"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

var (
	// MinLinecards is the minimum number of populated linecards the DUT must
	// expose.
	MinLinecards = flag.Int("mlc_min_linecards", 2, "Minimum number of populated linecards expected on the DUT.")
	// MinICsPerLinecard is the minimum number of forwarding integrated circuits
	// each populated linecard must expose.
	MinICsPerLinecard = flag.Int("mlc_min_ics_per_linecard", 2, "Minimum number of forwarding integrated circuits expected per linecard.")
)

// Naming describes how a vendor names linecard, integrated circuit and
// interface entities on a modular chassis. Each regexp's first submatch is the
// linecard slot number. For IC the second submatch is the chip index.
type Naming struct {
	Linecard  *regexp.Regexp
	IC        *regexp.Regexp
	Interface *regexp.Regexp
	// ICFormat formats an IC name from (slot, chip).
	ICFormat string
}

// ICName returns the conventional name of chip on the linecard in slot.
func (n Naming) ICName(slot, chip int) string {
	return fmt.Sprintf(n.ICFormat, slot, chip)
}

// namings holds the naming conventions per vendor. These match the patterns a
// controller uses to associate gNMI components with linecard slots.
var namings = map[ondatra.Vendor]Naming{
	ondatra.ARISTA: {
		Linecard:  regexp.MustCompile(`^Linecard(\d+)$`),
		IC:        regexp.MustCompile(`^SwitchChip(\d+)/(\d+)$`),
		Interface: regexp.MustCompile(`^Ethernet(\d+)/\d+(?:/\d+)?$`),
		ICFormat:  "SwitchChip%d/%d",
	},
	ondatra.NOKIA: {
		Linecard:  regexp.MustCompile(`^Linecard(\d+)$`),
		IC:        regexp.MustCompile(`^SwitchChip(\d+)/(\d+)$`),
		Interface: regexp.MustCompile(`^ethernet-(\d+)/\d+(?:/\d+)?$`),
		ICFormat:  "SwitchChip%d/%d",
	},
	ondatra.CISCO: {
		Linecard:  regexp.MustCompile(`^0/(\d+)/CPU0$`),
		IC:        regexp.MustCompile(`^0/(\d+)/CPU0-NPU(\d+)$`),
		Interface: regexp.MustCompile(`^[A-Za-z]+0/(\d+)/\d+/\d+(?:/\d+)?$`),
		ICFormat:  "0/%d/CPU0-NPU%d",
	},
	ondatra.JUNIPER: {
		Linecard:  regexp.MustCompile(`^(?:CHASSIS\d+:)?FPC(\d+)$`),
		IC:        regexp.MustCompile(`^(?:CHASSIS\d+:)?FPC(\d+):PIC\d+:NPU(\d+)$`),
		Interface: regexp.MustCompile(`^[a-z]+-(\d+)/\d+/\d+(?::\d+)?$`),
		ICFormat:  "FPC%d:PIC0:NPU%d",
	},
}

// NamingFor returns the naming conventions for the DUT's vendor. It returns
// false if no convention is known, in which case naming-specific checks are
// skipped while structural checks still run.
func NamingFor(dut *ondatra.DUTDevice) (Naming, bool) {
	n, ok := namings[dut.Vendor()]
	return n, ok
}

// SlotFromName returns the slot number captured by the first submatch of re.
func SlotFromName(re *regexp.Regexp, name string) (int, bool) {
	if re == nil {
		return 0, false
	}
	m := re.FindStringSubmatch(name)
	if len(m) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// Component is the subset of a platform component's state used by the tests.
type Component struct {
	Name       string
	Type       string
	Parent     string
	OperStatus string
	SlotID     string
	NodeID     uint64
	HasNodeID  bool
}

// Inventory is a snapshot of the DUT's component hierarchy.
type Inventory struct {
	Components map[string]*Component
	// HardwarePort maps an interface name to its state/hardware-port value.
	HardwarePort map[string]string
}

func keyName(p *gpb.Path, elem string) string {
	for _, e := range p.GetElem() {
		if e.GetName() == elem {
			return e.GetKey()["name"]
		}
	}
	return ""
}

func (inv *Inventory) comp(name string) *Component {
	c, ok := inv.Components[name]
	if !ok {
		c = &Component{Name: name}
		inv.Components[name] = c
	}
	return c
}

func typeString(u oc.Component_Type_Union) string {
	switch v := u.(type) {
	case oc.E_PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT:
		return v.String()
	case oc.E_PlatformTypes_OPENCONFIG_SOFTWARE_COMPONENT:
		return v.String()
	}
	return fmt.Sprint(u)
}

// lookupAll is gnmi.LookupAll with a fresh ONCE subscription per call, so each
// call observes the device's initial snapshot (data sent before sync_response).
func lookupAll[T any](t testing.TB, dut *ondatra.DUTDevice, q ygnmi.WildcardQuery[T]) []*ygnmi.Value[T] {
	t.Helper()
	return gnmi.LookupAll(t, dut, q)
}

// Discover takes a snapshot of the DUT's component and interface hierarchy.
// Every leaf is read with an independent ONCE subscription, so a leaf is only
// reported if the device includes it in the initial snapshot.
func Discover(t testing.TB, dut *ondatra.DUTDevice) *Inventory {
	t.Helper()
	inv := &Inventory{Components: map[string]*Component{}, HardwarePort: map[string]string{}}
	for _, v := range lookupAll(t, dut, gnmi.OC().ComponentAny().Type().State()) {
		if val, ok := v.Val(); ok {
			inv.comp(keyName(v.Path, "component")).Type = typeString(val)
		}
	}
	for _, v := range lookupAll(t, dut, gnmi.OC().ComponentAny().Parent().State()) {
		if val, ok := v.Val(); ok {
			inv.comp(keyName(v.Path, "component")).Parent = val
		}
	}
	for _, v := range lookupAll(t, dut, gnmi.OC().ComponentAny().OperStatus().State()) {
		if val, ok := v.Val(); ok {
			inv.comp(keyName(v.Path, "component")).OperStatus = val.String()
		}
	}
	for _, v := range lookupAll(t, dut, gnmi.OC().ComponentAny().Linecard().SlotId().State()) {
		if val, ok := v.Val(); ok {
			inv.comp(keyName(v.Path, "component")).SlotID = val
		}
	}
	for _, v := range lookupAll(t, dut, gnmi.OC().ComponentAny().IntegratedCircuit().NodeId().State()) {
		if val, ok := v.Val(); ok {
			c := inv.comp(keyName(v.Path, "component"))
			c.NodeID, c.HasNodeID = val, true
		}
	}
	for _, v := range lookupAll(t, dut, gnmi.OC().InterfaceAny().HardwarePort().State()) {
		if val, ok := v.Val(); ok {
			inv.HardwarePort[keyName(v.Path, "interface")] = val
		}
	}
	return inv
}

// ByType returns the sorted names of components with the given type.
func (inv *Inventory) ByType(typ oc.E_PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT) []string {
	var out []string
	for n, c := range inv.Components {
		if c.Type == typ.String() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Linecards returns the names of LINECARD components.
func (inv *Inventory) Linecards() []string {
	return inv.ByType(oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_LINECARD)
}

// ICs returns the names of INTEGRATED_CIRCUIT components.
func (inv *Inventory) ICs() []string {
	return inv.ByType(oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_INTEGRATED_CIRCUIT)
}

// ICsOnLinecard returns the names of INTEGRATED_CIRCUIT components whose parent
// is the given linecard.
func (inv *Inventory) ICsOnLinecard(lc string) []string {
	var out []string
	for _, ic := range inv.ICs() {
		if inv.Components[ic].Parent == lc {
			out = append(out, ic)
		}
	}
	return out
}

// OwningIC walks interface -> hardware-port -> PORT component -> parent and
// returns the owning integrated circuit. It returns a description of where the
// chain broke if it cannot be resolved.
func (inv *Inventory) OwningIC(intf string) (string, error) {
	hp, ok := inv.HardwarePort[intf]
	if !ok || hp == "" {
		return "", fmt.Errorf("interface %q has no state/hardware-port", intf)
	}
	port, ok := inv.Components[hp]
	if !ok {
		return "", fmt.Errorf("interface %q hardware-port %q is not a known component", intf, hp)
	}
	if port.Type != oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_PORT.String() {
		return "", fmt.Errorf("interface %q hardware-port %q has type %q, want PORT", intf, hp, port.Type)
	}
	if port.Parent == "" {
		return "", fmt.Errorf("PORT component %q has no state/parent", hp)
	}
	ic, ok := inv.Components[port.Parent]
	if !ok || ic.Type != oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_INTEGRATED_CIRCUIT.String() {
		typ := "<unknown>"
		if ok {
			typ = ic.Type
		}
		return "", fmt.Errorf("PORT component %q parent %q has type %s, want INTEGRATED_CIRCUIT", hp, port.Parent, typ)
	}
	return port.Parent, nil
}

// PortsByIC returns, for every integrated circuit, the PORT components whose
// parent is that IC.
func (inv *Inventory) PortsByIC() map[string][]string {
	out := map[string][]string{}
	for n, c := range inv.Components {
		if c.Type == oc.PlatformTypes_OPENCONFIG_HARDWARE_COMPONENT_PORT.String() && c.Parent != "" {
			out[c.Parent] = append(out[c.Parent], n)
		}
	}
	return out
}

// EnablePorts administratively enables every DUT port in the testbed.
func EnablePorts(t testing.TB, dut *ondatra.DUTDevice) {
	t.Helper()
	b := &gnmi.SetBatch{}
	for _, p := range dut.Ports() {
		name, enabled := p.Name(), true
		gnmi.BatchUpdate(b, gnmi.OC().Interface(p.Name()).Config(), &oc.Interface{
			Name:    &name,
			Type:    oc.IETFInterfaces_InterfaceType_ethernetCsmacd,
			Enabled: &enabled,
		})
	}
	b.Set(t, dut)
}

// DUTPortSlots returns the linecard slot of every DUT port in the testbed,
// keyed by interface name, using the vendor's interface naming convention.
func DUTPortSlots(t testing.TB, dut *ondatra.DUTDevice) map[string]int {
	t.Helper()
	n, ok := NamingFor(dut)
	out := map[string]int{}
	for _, p := range dut.Ports() {
		if !ok {
			continue
		}
		if s, ok := SlotFromName(n.Interface, p.Name()); ok {
			out[p.Name()] = s
		}
	}
	return out
}
