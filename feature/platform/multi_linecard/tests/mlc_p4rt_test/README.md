# PLT-MLC-2: Multi-linecard readiness: per-chip P4Runtime and packet I/O

## Summary

Checks that every forwarding chip can be programmed as an independent P4Runtime device on the chassis endpoint and that punted packets are delivered on the stream of the chip that owns the ingress port.

This test is part of the multi-linecard readiness suite. See
[the suite README](../../README.md) for the topology, flags, naming
conventions and the full requirement descriptions.

## Testbed type

*   [`TESTBED_DUT_ATE_4LINKS`](https://github.com/openconfig/featureprofiles/blob/main/topologies/atedut_4.testbed)

Place one DUT port on each forwarding chip of two different linecards.

## Procedure

Each subtest is named after the requirement it checks:

*   `MLC-P4RT-001`
*   `MLC-P4RT-002`
*   `MLC-DP-002`
*   `MLC-P4RT-003`
*   `MLC-P4RT-004`

## OpenConfig Path and RPC Coverage

```yaml
paths:
  /components/component/integrated-circuit/config/node-id:
  /interfaces/interface/config/id:
  /interfaces/interface/state/id:
  /interfaces/interface/state/counters/in-pkts:

rpcs:
  gnmi:
    gNMI.Set:
    gNMI.Subscribe:
  p4rt:
    P4Runtime.StreamChannel:
    P4Runtime.SetForwardingPipelineConfig:
    P4Runtime.GetForwardingPipelineConfig:
    P4Runtime.Write:
    P4Runtime.Read:
```

## Required DUT platform

*   Modular chassis with at least two linecards and at least two forwarding
    integrated circuits per linecard, physical or emulated.
