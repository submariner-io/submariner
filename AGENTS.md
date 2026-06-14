# Submariner

Development guidelines for AI coding assistants working with the Submariner repository.

## Overview

Submariner enables direct networking between Pods and Services in different
Kubernetes clusters, on-premises or in the cloud.

**Key capabilities:**

- Layer 3 connectivity via encrypted tunnels (IPsec, WireGuard, VXLAN)
- Service discovery across clusters (via Lighthouse)
- Support for overlapping CIDRs (via Globalnet)
- Multiple CNI compatibility (OVN-Kubernetes, Calico, kindnet, etc.)

## Quick Component Orientation

**Three main binaries:**

- **Gateway** (`main.go`) — tunnel establishment, leader election, NAT discovery
- **Route Agent** (`pkg/routeagent_driver/main.go`) — runs on every node, programs routes and packet filter rules
- **Globalnet** (`pkg/globalnet/main.go`) — handles overlapping CIDRs via SNAT/DNAT

**External dependencies:**

- **Broker** (submariner-operator repo) — metadata exchange between clusters
- **Lighthouse** (lighthouse repo) — service discovery

## Architecture

@ARCHITECTURE.md

Quick reference for code navigation:

- **Gateway entry:** `main.go`
- **Route Agent entry:** `pkg/routeagent_driver/main.go`
- **Event handlers:** `pkg/routeagent_driver/handlers/<name>/`
- **Cable drivers:** `pkg/cable/<name>/`
- **API types:** `pkg/apis/submariner.io/v1/`

## Datapath Architecture

For runtime packet flows, routing tables, and network topology details:

- [Datapath Architecture](https://github.com/submariner-io/submariner-diagnostics/blob/devel/docs/analysis/datapath-architecture.md) —
  Non-OVN vs OVN datapaths, asymmetric traffic flows
- [Tunnel Analysis](https://github.com/submariner-io/submariner-diagnostics/blob/devel/docs/analysis/tunnel-analysis.md) —
  IPsec tunnel connectivity
- [RouteAgent Analysis](https://github.com/submariner-io/submariner-diagnostics/blob/devel/docs/analysis/routeagent-analysis.md) —
  OVN-specific checks

## Extension Patterns

### Adding an Event Handler

Event handlers react to cluster events (endpoints, nodes, transitions).

**Steps:**

1. Create package: `pkg/routeagent_driver/handlers/<name>/`
2. Embed `event.HandlerBase` in your struct
3. Implement `GetName()` and `GetNetworkPlugins()`
4. Override lifecycle methods (e.g., `TransitionToGateway`, `RemoteEndpointCreated`)
5. Register in `pkg/routeagent_driver/main.go` via `event.NewRegistry(..., handlers...)`

**Existing handlers:**

- OVN (`handlers/ovn/`) — OVN-Kubernetes only
- kube-proxy (`handlers/kubeproxy/`) — all non-OVN CNIs
- Calico (`handlers/calico/`) — Calico IPPool management
- MTU (`handlers/mtu/`) — all CNIs
- Health checker (`handlers/healthchecker/`) — all CNIs

### Adding a Cable Driver

Cable drivers establish tunnels (IPsec, WireGuard, VXLAN).

**Steps:**

1. Create package: `pkg/cable/<name>/`
2. Implement `cable.Driver` interface (`pkg/cable/driver.go`)
3. Register via `cable.AddDriver(name, constructor)` in `init()`

**Existing drivers:**

- libreswan (IPsec) — default
- wireguard
- vxlan

## Code Patterns

### Logging

**Use:** `github.com/submariner-io/admiral/pkg/log`

```go
var logger = log.Logger{Logger: logf.Log.WithName("component-name")}
logger.Infof("message %v", val)
logger.Errorf(err, "message")
```

**Do NOT use:** `fmt.Println`, `log.Printf`, `slog`

### Packet Filter Rules

**Use:** `pkg/packetfilter.Interface`

**Do NOT call:** iptables/nftables commands directly

The abstraction layer handles both iptables and nftables backends automatically.

### Testing

- **Unit tests:** Ginkgo/Gomega, fakes in `fake/` subdirs
- **E2E tests:** Shipyard kind clusters
- **Testing details:** See @ARCHITECTURE.md Testing section

## Workflows

### Commit Messages

@.agents/commit-templates.md

### CVE Fixes

@.agents/workflows/cve-fix.md

### Konflux Component Setup

@.agents/workflows/konflux-component-setup.md

### Linting

**Required before committing:**

```bash
make markdownlint  # MUST run after editing any .md file
make lint          # Go linters
```

## Build Commands

```bash
make build         # Build all binaries
make unit          # Run unit tests
make e2e           # Run E2E tests (kindnet CNI)
make e2e using=ovn # Run E2E tests (OVN CNI)
make clean         # Remove build artifacts
```

## Troubleshooting

For production cluster debugging and offline diagnostics analysis:

- [submariner-diagnostics](https://github.com/submariner-io/submariner-diagnostics)
- Use `/submariner:analyze-offline` skill for collected diagnostics

## External References

- **Website:** <https://submariner.io>
- **Documentation:** <https://submariner.io/getting-started/>
- **Broker:** <https://github.com/submariner-io/submariner-operator>
- **Lighthouse:** <https://github.com/submariner-io/lighthouse>
- **Shipyard:** <https://github.com/submariner-io/shipyard>
