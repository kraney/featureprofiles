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
    platform_type: ["CHASSIS", "LINECARD", "INTEGRATED_CIRCUIT", "PORT"]
  /components/component/state/parent:
    platform_type: ["LINECARD", "INTEGRATED_CIRCUIT", "PORT"]
  /components/component/state/oper-status:
    platform_type: ["LINECARD", "INTEGRATED_CIRCUIT"]
  /components/component/linecard/state/slot-id:
    platform_type: ["LINECARD"]
  /components/component/integrated-circuit/config/node-id:
    platform_type: ["INTEGRATED_CIRCUIT"]
  /components/component/integrated-circuit/state/node-id:
    platform_type: ["INTEGRATED_CIRCUIT"]
  /components/component/integrated-circuit/backplane-facing-capacity/state/total-operational-capacity:
    platform_type: ["INTEGRATED_CIRCUIT"]
  /interfaces/interface/config/enabled:
  /interfaces/interface/state/oper-status:
  /interfaces/interface/state/hardware-port:
  /interfaces/interface/ethernet/state/port-speed:

rpcs:
  gnmi:
    gNMI.Set:
    gNMI.Subscribe:
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
          "state": {
            "slot-id": "0"
          }
        },
        "name": "Linecard0",
        "state": {
          "oper-status": "openconfig-platform-types:ACTIVE",
          "type": "openconfig-platform-types:LINECARD"
        }
      },
      {
        "config": {
          "name": "SwitchChip0/0"
        },
        "integrated-circuit": {
          "backplane-facing-capacity": {
            "state": {
              "total-operational-capacity": "400000000000"
            }
          },
          "config": {
            "node-id": "1000"
          },
          "state": {
            "node-id": "1000"
          }
        },
        "name": "SwitchChip0/0",
        "state": {
          "parent": "Linecard0",
          "type": "openconfig-platform-types:INTEGRATED_CIRCUIT"
        }
      }
    ]
  }
}
```

## Required DUT platform

*   Modular chassis with at least two linecards and at least two forwarding
    integrated circuits per linecard, physical or emulated.
