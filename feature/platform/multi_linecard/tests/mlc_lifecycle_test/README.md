# PLT-MLC-4: Multi-linecard readiness: linecard and chassis lifecycle

## Summary

Checks that a single linecard can be rebooted or power-cycled without affecting the others, that its state changes stream ON_CHANGE, and that a chassis cold reboot restores every linecard, chip and port.

This test is part of the multi-linecard readiness suite. See
[the suite README](../../README.md) for the topology, flags, naming
conventions and the full requirement descriptions.

## Testbed type

*   [`TESTBED_DUT_ATE_4LINKS`](https://github.com/openconfig/featureprofiles/blob/main/topologies/atedut_4.testbed)

Place one DUT port on each forwarding chip of two different linecards.

## Procedure

Each subtest is named after the requirement it checks:

*   `MLC-LC-003`
*   `MLC-LC-002`
*   `MLC-LC-001`
*   `MLC-KNE-004`

## OpenConfig Path and RPC Coverage

```yaml
paths:
  /components/component/state/oper-status:
    platform_type: ["LINECARD"]
  /components/component/linecard/config/power-admin-state:
    platform_type: ["LINECARD"]
  /components/component/integrated-circuit/state/node-id:
    platform_type: ["INTEGRATED_CIRCUIT"]
  /interfaces/interface/state/oper-status:

rpcs:
  gnmi:
    gNMI.Set:
    gNMI.Subscribe:
  gnoi:
    system.System.Reboot:
  p4rt:
    P4Runtime.StreamChannel:
```

#### Canonical OC

```json
{
  "components": {
    "component": [
      {
        "config": {
          "name": "Linecard0"
        },
        "linecard": {
          "config": {
            "power-admin-state": "POWER_DISABLED"
          }
        },
        "name": "Linecard0"
      }
    ]
  }
}
```

## Required DUT platform

*   Modular chassis with at least two linecards and at least two forwarding
    integrated circuits per linecard, physical or emulated.
