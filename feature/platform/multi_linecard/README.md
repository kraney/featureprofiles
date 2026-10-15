# Multi-linecard readiness

## Summary

These tests check whether a modular (multi-linecard) device, physical or
emulated, exposes and behaves like a chassis with several independently managed
forwarding chips. An SDN controller that programs each forwarding chip as its
own P4Runtime device needs this.

Each subtest is named after a requirement ID (`MLC-*`) in the table below, so a
run reports pass or fail per requirement. Run them as a vendor self-check: a
device is multi-linecard ready when every requirement passes with no
deviations.

The tests target a chassis with at least two populated linecards and at least
two forwarding integrated circuits (ICs) per linecard. They run on KNE
emulations and on hardware.

## Topology

`TESTBED_DUT_ATE_4LINKS`. Place one DUT port on each forwarding IC of two
different linecards, so the four links cover every IC on both linecards. Choose
the DUT ports accordingly: for example, on an Arista DCS-7808 with
DCS-7800R3AK-36DM linecards in slots 3 and 4 use `Ethernet3/1/1`,
`Ethernet3/19/1`, `Ethernet4/1/1` and `Ethernet4/19/1`.

KNE topologies:

* Arista cEOS: [topologies/kne/arista/ceos/mlc_dutate.textproto](../../../topologies/kne/arista/ceos/mlc_dutate.textproto)
* Nokia SR Linux: [topologies/kne/nokia/srlinux/mlc_dutate.textproto](../../../topologies/kne/nokia/srlinux/mlc_dutate.textproto)

The tests do not exclude other vendors. The IC and linecard naming conventions
for Cisco and Juniper are built in (see `internal/mlc`). Without a matching
convention the tests skip only the naming checks.

## Flags

| Flag                         | Default | Meaning                                         |
| ---------------------------- | ------- | ----------------------------------------------- |
| `-mlc_min_linecards`         | 2       | Populated linecards the DUT must expose.        |
| `-mlc_min_ics_per_linecard`  | 2       | Forwarding ICs each populated linecard exposes. |

## Running on KNE

```shell
kne create topologies/kne/arista/ceos/mlc_dutate.textproto
go test ./feature/platform/multi_linecard/... \
  -kne-topo $PWD/topologies/kne/arista/ceos/mlc_dutate.textproto \
  -vendor_creds ARISTA/admin/admin
```

Use `-run 'TestMLCInventory/MLC-INV-002'` to iterate on a single requirement.
`mlc_lifecycle_test` reboots the DUT and waits for linecard events, so give it
`-timeout 90m`.

## Tests

| Test                          | Plan ID   | Covers                                        |
| ----------------------------- | --------- | --------------------------------------------- |
| `tests/mlc_inventory_test`    | PLT-MLC-1 | L0 and L1: instantiation, inventory, telemetry |
| `tests/mlc_p4rt_test`         | PLT-MLC-2 | L2 and L3: per-IC P4Runtime, packet I/O        |
| `otg_tests/mlc_dataplane_test`| PLT-MLC-3 | L1, L2 and L3: cross-linecard forwarding, LAG, gRIBI |
| `tests/mlc_lifecycle_test`    | PLT-MLC-4 | L4: linecard and chassis lifecycle             |

## Requirements

Priority: **P0** blocks the controller from bringing the chips up at all. **P1**
is needed by a functional test path. **P2** is only needed to remove an existing
controller-side workaround, or is informational.

| ID | Pri | Requirement | Checked by |
| -- | --- | ----------- | ---------- |
| MLC-KNE-001 | P0 | A modular multi-linecard chassis can be declared from the KNE topology alone, with no out-of-band patching. | Topology files; precondition of every test |
| MLC-KNE-002 | P0 | Linecard slot numbers match the real chassis (for example DCS-7808 slots 3-10, IXR-10e slots 1-8), and `linecard/state/slot-id` matches the name. | `TestMLCInventory/MLC-KNE-002` |
| MLC-KNE-003 | P0 | Every port on every populated linecard binds to its veth and comes up; never `NOT_PRESENT`. | `TestMLCInventory/MLC-KNE-003` |
| MLC-KNE-004 | P1 | The declared linecard population survives a container restart or reboot. | `TestMLCLifecycle/MLC-LC-001/MLC-KNE-004` |
| MLC-KNE-005 | P2 | Documented, stable resource footprint for multi-slot nodes. | Observed during all tests |
| MLC-INV-001 | P0 | A `LINECARD` component exists per populated slot, with `linecard/state/slot-id`. | `TestMLCInventory/MLC-INV-001` |
| MLC-INV-002 | P0 | `/components/component[name=<linecard>]/state/oper-status` is `ACTIVE` in every initial Subscribe snapshot (before `sync_response`), including resubscriptions. | `TestMLCInventory/MLC-INV-002` |
| MLC-INV-003 | P0 | One `INTEGRATED_CIRCUIT` component per forwarding chip, with `state/parent` set to its linecard. | `TestMLCInventory/MLC-INV-003` |
| MLC-INV-004 | P0 | `integrated-circuit/config/node-id` is accepted on every IC, and `state/node-id` is streamed in every initial snapshot. | `TestMLCInventory/MLC-INV-004` |
| MLC-INV-005 | P1 | Interface `state/hardware-port` points to a `PORT` component whose `state/parent` is the IC that owns the port. Ports are split across ICs as on real hardware. | `TestMLCInventory/MLC-INV-005` |
| MLC-INV-006 | P0 | Interfaces on every linecard use modular names and stream oper-status and port-speed. | `TestMLCInventory/MLC-INV-006` |
| MLC-INV-007 | P1 | Interface counters stream for interfaces on every linecard. | `TestMLCDataplane/MLC-INV-007` |
| MLC-INV-008 | P1 | A LAG with members on different linecards comes up and reports every member. | `TestMLCDataplane/MLC-INV-008` |
| MLC-INV-009 | P2 | `FABRIC` components with oper-status. | `TestMLCInventory/MLC-INV-009` |
| MLC-INV-010 | P2 | Each IC streams `integrated-circuit/backplane-facing-capacity/state/*`. | `TestMLCInventory/MLC-INV-010` |
| MLC-INV-011 | P2 | Identityref values under PROTO encoding are unprefixed (`ACTIVE`, not `openconfig-platform-types:ACTIVE`). | `TestMLCInventory/MLC-INV-011` |
| MLC-P4RT-001 | P0 | One independent P4Runtime StreamChannel per IC (`device_id` = node-id) on the chassis endpoint. Closing one does not affect the others. | `TestMLCP4RT/MLC-P4RT-001` |
| MLC-P4RT-002 | P1 | Pipeline push, table write and table read succeed independently per IC. | `TestMLCP4RT/MLC-P4RT-002` |
| MLC-P4RT-003 | P1 | Slot-encoded 32-bit interface IDs (65536 and up) are accepted and used as the P4Runtime port in PacketIn metadata. | `TestMLCP4RT/MLC-P4RT-003` |
| MLC-P4RT-004 | P1 | A new primary with a higher election ID takes over each IC; the old primary is notified; no component or port state changes. | `TestMLCP4RT/MLC-P4RT-004` |
| MLC-GRIBI-001 | P1 | gRIBI next-hop groups with next hops on different linecards program and forward. | `TestMLCDataplane/MLC-GRIBI-001` |
| MLC-GRIBI-002 | P2 | gRIBI entries survive a client reconnect with a higher election ID. | `TestMLCDataplane/MLC-GRIBI-002` |
| MLC-DP-001 | P1 | Traffic is forwarded between ports on different linecards and ICs. | `TestMLCDataplane/MLC-DP-001` |
| MLC-DP-002 | P1 | A punted packet arrives only on the stream of the IC that owns the ingress port. Works for LLDP on every linecard. | `TestMLCP4RT/MLC-DP-002` |
| MLC-DP-003 | P2 | Cross-linecard forwarding continues while controller sessions reconnect. | `TestMLCDataplane/MLC-DP-003` |
| MLC-LC-001 | P1 | gNOI `System.Reboot(COLD)` restarts the chassis. Afterwards every linecard is `ACTIVE`, every IC streams node-id and arbitrates, and every port is `UP`. | `TestMLCLifecycle/MLC-LC-001` |
| MLC-LC-002 | P1 | Linecard oper-status changes stream ON_CHANGE. Its interfaces send oper-status updates, not only deletes. Other linecards are unaffected. | `TestMLCLifecycle/MLC-LC-002` |
| MLC-LC-003 | P2 | gNOI `System.Reboot` of a linecard subcomponent affects only that linecard. | `TestMLCLifecycle/MLC-LC-003` |
| MLC-LC-004 | P1 | Disabling a port on any linecard affects only that port. | `TestMLCInventory/MLC-LC-004` |

## Naming conventions

A controller associates components with linecards by name. The tests expect the
following names and skip only the naming checks for vendors not listed.

| Vendor  | Linecard                    | IC                       | Interface slot      |
| ------- | --------------------------- | ------------------------ | ------------------- |
| Arista  | `Linecard<slot>`            | `SwitchChip<slot>/<chip>`| `Ethernet<slot>/...`|
| Nokia   | `Linecard<slot>`            | `SwitchChip<slot>/<chip>`| `ethernet-<slot>/...`|
| Cisco   | `0/<slot>/CPU0`             | `0/<slot>/CPU0-NPU<chip>`| `...0/<slot>/...`   |
| Juniper | `FPC<slot>`                 | `FPC<slot>:PIC0:NPU<chip>`| `xx-<slot>/...`    |

## OpenConfig Path and RPC Coverage

```yaml
paths:
  /components/component/state/type:
  /components/component/state/parent:
  /components/component/state/oper-status:
  /components/component/linecard/state/slot-id:
  /components/component/linecard/config/power-admin-state:
  /components/component/integrated-circuit/config/node-id:
  /components/component/integrated-circuit/state/node-id:
  /components/component/integrated-circuit/backplane-facing-capacity/state/total-operational-capacity:
  /interfaces/interface/config/enabled:
  /interfaces/interface/config/id:
  /interfaces/interface/state/id:
  /interfaces/interface/state/oper-status:
  /interfaces/interface/state/hardware-port:
  /interfaces/interface/ethernet/state/port-speed:
  /interfaces/interface/state/counters/in-pkts:
  /interfaces/interface/state/counters/out-pkts:
  /interfaces/interface/ethernet/config/aggregate-id:
  /lacp/interfaces/interface/members/member/state/interface:

rpcs:
  gnmi:
    gNMI.Get:
    gNMI.Set:
    gNMI.Subscribe:
  gnoi:
    system.System.Reboot:
  p4rt:
    P4Runtime.StreamChannel:
    P4Runtime.SetForwardingPipelineConfig:
    P4Runtime.GetForwardingPipelineConfig:
    P4Runtime.Write:
    P4Runtime.Read:
  gribi:
    gRIBI.Modify:
```

## Required DUT platform

* Modular chassis with at least two linecards and at least two forwarding ICs
  per linecard, physical or emulated.
