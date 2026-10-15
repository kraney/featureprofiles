# PLT-MLC-3: Multi-linecard readiness: cross-linecard forwarding, gRIBI and LAG

## Summary

Checks forwarding between ports on different linecards and chips, gRIBI next-hop groups whose next hops span linecards (including across a gRIBI client reconnect), and a LAG whose members sit on different linecards.

This test is part of the multi-linecard readiness suite. See
[the suite README](../../README.md) for the topology, flags, naming
conventions and the full requirement descriptions.

## Testbed type

*   [`TESTBED_DUT_ATE_4LINKS`](https://github.com/openconfig/featureprofiles/blob/main/topologies/atedut_4.testbed)

Place one DUT port on each forwarding chip of two different linecards.

## Procedure

Each subtest is named after the requirement it checks:

*   `MLC-DP-001`
*   `MLC-INV-007`
*   `MLC-GRIBI-001`
*   `MLC-GRIBI-002`
*   `MLC-DP-003`
*   `MLC-INV-008`

## OpenConfig Path and RPC Coverage

```yaml
paths:
  /interfaces/interface/state/counters/in-pkts:
  /interfaces/interface/state/counters/out-pkts:
  /interfaces/interface/ethernet/config/aggregate-id:
  /interfaces/interface/ethernet/state/aggregate-id:
  /interfaces/interface/aggregation/state/member:
  /interfaces/interface/state/oper-status:

rpcs:
  gnmi:
    gNMI.Set:
    gNMI.Subscribe:
  gribi:
    gRIBI.Modify:
    gRIBI.Get:
```

## Required DUT platform

*   Modular chassis with at least two linecards and at least two forwarding
    integrated circuits per linecard, physical or emulated.
