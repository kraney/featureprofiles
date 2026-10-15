# PLT-MLC-1: Multi-linecard readiness: inventory and telemetry

## Summary

Checks that each populated linecard, forwarding chip and port of a modular DUT is exposed through OpenConfig with the hierarchy, naming and initial-snapshot behavior a per-chip controller depends on.

This test is part of the multi-linecard readiness suite. See
[the suite README](../../README.md) for the topology, flags, naming
conventions and the full requirement descriptions.

## Testbed type

*   [`TESTBED_DUT_ATE_4LINKS`](https://github.com/openconfig/featureprofiles/blob/main/topologies/atedut_4.testbed)

Place one DUT port on each forwarding chip of two different linecards.

## Procedure

Each subtest is named after the requirement it checks:

*   `MLC-INV-001`
*   `MLC-KNE-002`
*   `MLC-INV-002`
*   `MLC-INV-003`
*   `MLC-INV-004`
*   `MLC-INV-006`
*   `MLC-KNE-003`
*   `MLC-INV-012`
*   `MLC-INV-005`
*   `MLC-INV-009`
*   `MLC-INV-010`
*   `MLC-INV-011`
*   `MLC-LC-004`

## OpenConfig Path and RPC Coverage

```yaml
paths:
  /components/component/state/type:
  /components/component/state/parent:
  /components/component/state/oper-status:
  /components/component/linecard/state/slot-id:
  /components/component/integrated-circuit/config/node-id:
  /components/component/integrated-circuit/state/node-id:
  /components/component/integrated-circuit/backplane-facing-capacity/state/total-operational-capacity:
  /interfaces/interface/config/enabled:
  /interfaces/interface/state/oper-status:
  /interfaces/interface/state/hardware-port:
  /interfaces/interface/ethernet/state/port-speed:

rpcs:
  gnmi:
    gNMI.Set:
    gNMI.Subscribe:
```

## Required DUT platform

*   Modular chassis with at least two linecards and at least two forwarding
    integrated circuits per linecard, physical or emulated.
