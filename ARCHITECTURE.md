# Submariner Architecture

## What is Submariner?

Submariner connects Kubernetes clusters by creating tunnels between them
(encrypted via IPsec/WireGuard, or unencrypted via VXLAN), enabling pod-to-pod
and pod-to-service communication across clusters.

For detailed datapath architecture (packet flows, VXLAN, routing tables, OVN
logical router policies), see the submariner-diagnostics repository:

- [Datapath Architecture](https://github.com/submariner-io/submariner-diagnostics/blob/devel/docs/analysis/datapath-architecture.md)
- [Tunnel Analysis](https://github.com/submariner-io/submariner-diagnostics/blob/devel/docs/analysis/tunnel-analysis.md)
- [RouteAgent Analysis](https://github.com/submariner-io/submariner-diagnostics/blob/devel/docs/analysis/routeagent-analysis.md)

This document focuses on code architecture, component structure, and extension
patterns for developers working on Submariner.

---

## Binaries / Components

### 1. Gateway (`main.go`)

**Entry point:** `main.go` (repo root)
**Dockerfile:** `package/Dockerfile.submariner-gateway`

Can run on one or more nodes per cluster, but only one is **active** at a time
(decided by leader election). Other nodes are standby candidates. The active
gateway node is responsible for:

- Establishing and maintaining IPsec/WireGuard/VXLAN tunnels to remote clusters
- Leader election via `submariner-gateway-lock` (K8s lease)
- NAT discovery to determine the correct IP for tunnel endpoints
- Syncing endpoint info to/from the broker

Key packages:

- `pkg/gateway/` — main gateway lifecycle, leader election
- `pkg/cableengine/` — manages cable drivers, health checking
- `pkg/cable/` — cable driver interface + implementations
  - `pkg/cable/libreswan/` — IPsec via Libreswan (default)
  - `pkg/cable/wireguard/` — WireGuard driver
  - `pkg/cable/vxlan/` — VXLAN driver
- `pkg/natdiscovery/` — UDP-based NAT discovery protocol (protobuf)
- `pkg/controllers/` — tunnel controller, datastore syncer
- `pkg/endpoint/` — local endpoint info construction

---

### 2. Route Agent (`pkg/routeagent_driver/main.go`)

**Entry point:** `pkg/routeagent_driver/main.go`
**Dockerfile:** `package/Dockerfile.submariner-route-agent`

Runs on **every node** in the cluster. Responsible for:

- Programming routes so non-gateway nodes can reach remote clusters via the
  gateway
- Managing iptables/nftables rules for traffic steering
- OVN-Kubernetes integration (logical router policies, static routes)
- Health checking of remote endpoints

Uses an **event-driven handler architecture**:

- `pkg/event/` — handler interface + registry + controller
  - `event.Handler` interface: each handler implements Init, Stop, Uninstall,
    plus endpoint/node lifecycle callbacks
  - `event.HandlerBase` — embed this to get no-op defaults for optional methods
  - `pkg/event/controller/` — watches K8s resources, fires events to handlers
  - `pkg/event/registry.go` — registers handlers by network plugin

Handlers registered in `pkg/routeagent_driver/main.go`:

| Handler               | Package                   | CNI scope                         |
|-----------------------|---------------------------|-----------------------------------|
| OVN                   | `handlers/ovn/`           | OVN-Kubernetes only               |
| kube-proxy            | `handlers/kubeproxy/`     | All CNIs except OVN-Kubernetes    |
| Calico                | `handlers/calico/`        | Calico only                       |
| MTU                   | `handlers/mtu/`           | All CNIs (`AnyNetworkPlugin`)     |
| Health checker        | `handlers/healthchecker/` | All CNIs (`AnyNetworkPlugin`)     |
| Cable driver cleanup  | `cabledriver/`            | All CNIs (vxlan/xfrm cleanup)     |

CNI scope is declared via `GetNetworkPlugins() []string` on each handler.
`event.AnyNetworkPlugin` means the handler runs regardless of CNI.

---

### 3. Globalnet (`pkg/globalnet/main.go`)

**Entry point:** `pkg/globalnet/main.go`
**Dockerfile:** `package/Dockerfile.submariner-globalnet`

Runs on the gateway node when clusters have overlapping CIDRs. Responsible for:

- Assigning global IPs to pods/services that need cross-cluster reachability
- Programming iptables SNAT/DNAT rules via global IPs
- Watching K8s resources (pods, services, endpoints) and assigning GlobalEgressIPs

Key packages:

- `pkg/globalnet/controllers/` — individual controllers per resource type
  - `gateway_monitor.go` — starts/stops controllers based on gateway status
  - `gateway_controller.go` — handles gateway node changes
  - `cluster_egressip_controller.go` — ClusterGlobalEgressIP CRD
  - `global_egressip_controller.go` — GlobalEgressIP CRD
  - `service_controller.go` — GlobalIngressIP for services
  - `ingress_pod_controller.go` — pod ingress global IPs
  - `endpoints_controller.go` — remote endpoint handling
  - `uninstall.go` — cleanup on uninstall
- `pkg/globalnet/constants/` — shared constants
- `pkg/globalnet/metrics/` — Prometheus metrics

---

### 4. Await Node Ready (`pkg/await_node_ready/main.go`)

Small init container that waits for a node to be ready before the main
container starts. Not a core component.

---

### 5. Broker

The broker is **not in this repo** — it lives in
[submariner-operator](https://github.com/submariner-io/submariner-operator).

It is a central Kubernetes API server (or namespace) that gateways use to
exchange endpoint and cluster metadata across clusters. Each gateway syncs its
local `Endpoint` and `Cluster` CRs to the broker, and watches for remote
cluster objects written by other gateways.

Key interactions from this repo:

- `pkg/controllers/datastoresyncer/` — syncs local endpoint/cluster to broker
  and pulls remote endpoints/clusters back down

---

### 6. Service Discovery (Lighthouse)

Service discovery is **not in this repo** — it lives in
[submariner-io/lighthouse](https://github.com/submariner-io/lighthouse).

Lighthouse enables DNS-based service lookup across clusters using the
Kubernetes MCS (Multi-Cluster Services) API:

- Users create `ServiceExport` to make a service discoverable
- Lighthouse creates `ServiceImport` internally
- Services resolve as `<service>.<ns>.svc.clusterset.local`

This repo's contribution is the `Gateway` CR status, written by
`pkg/cableengine/syncer/` every 5 seconds. Lighthouse reads this to determine
cross-cluster reachability. The status contains:

- `HAStatus` — whether this gateway is active or passive
- `Connections[]` — per-tunnel connection status (`connected`, `connecting`,
  `error`), remote endpoint info, NAT details, and latency RTT
- `StatusFailure` — any error string if the cable engine is unhealthy

Lighthouse uses the connection status to decide whether a remote cluster's
services are currently reachable.

---

## Core API Types (`pkg/apis/submariner.io/v1/`)

Key CRDs:

- `Cluster` — represents a connected cluster (ClusterID, CIDRs, GlobalCIDR)
- `Endpoint` — represents a gateway endpoint (ClusterID, CableName, Subnets,
  Backend, NAT IPs)
- `Submariner` — top-level CR with cluster config
- `GlobalEgressIP` / `ClusterGlobalEgressIP` — global IP assignments
- `GlobalIngressIP` — inbound global IP for services/pods

Generated client in `pkg/client/` (do not edit manually).

---

## OVN Handler Deep Dive (`pkg/routeagent_driver/handlers/ovn/`)

Most active area of development. Handles routing when CNI is OVN-Kubernetes.

Key files:

- `handler.go` — `Handler` struct, implements `event.Handler`, connects to OVSDB
- `gateway_route_handler.go` — programs routes on the gateway node
- `gateway_route_controller.go` — controller driving gateway route reconciliation
- `non_gateway_route_handler.go` — programs routes on non-gateway nodes
- `non_gateway_route_controller.go` — controller for non-gateway route reconciliation
- `ovn_logical_routes.go` — manages OVN Logical Router Policies (LRPs) and
  static routes in the OVN NorthDB
- `south_rules.go` — OVN southbound rules
- `subnets.go` — subnet tracking
- `transit_switch_ip.go` — manages the transit switch IP for inter-cluster traffic
- `host_networking.go` — host network rules
- `connection.go` — OVSDB connection management
- `constants.go` — OVN-specific constants
- `uninstall.go` — cleanup on uninstall
- `vsctl/` — OVS vsctl wrapper
- `fake/` — test fakes

---

## Handler Deep Dives

### kube-proxy Handler (`pkg/routeagent_driver/handlers/kubeproxy/`)

Handles routing for all non-OVN CNIs (Flannel, Weave, Canal, etc.).

Key files:

- `kp_packetfilter.go` — `SyncHandler` struct, iptables/nftables rules for
  cross-cluster traffic
- `vxlan.go` — VXLAN tunnel setup to the gateway node
- `gw_transition.go` — handles gateway transition events
- `endpoint_handler.go` — remote endpoint created/removed logic
- `node_handler.go` — node join/leave handling
- `routes_iface.go` — route programming interface
- `packetfilter_iface.go` — packet filter rule interface
- `uninstall.go` — cleanup on uninstall

---

### Calico Handler (`pkg/routeagent_driver/handlers/calico/`)

Calico-specific handler that manages Calico IPPool resources for remote cluster
CIDRs, so Calico does not block cross-cluster traffic.

Key files:

- `ippool_handler.go` — creates/deletes Calico IPPool CRs for remote subnets
  on `RemoteEndpointCreated` / `RemoteEndpointRemoved`

---

### MTU Handler (`pkg/routeagent_driver/handlers/mtu/`)

Applies to **all CNIs**. Sets the correct MTU on the VXLAN interface to account
for tunnel overhead, preventing packet fragmentation across clusters.

Key files:

- `mtuhandler.go` — calculates and programs MTU on local interfaces

---

### Health Checker Handler (`pkg/routeagent_driver/handlers/healthchecker/`)

Applies to **all CNIs**. Pings remote endpoint IPs to detect connectivity loss
and updates `Connection.Status` accordingly.

Key files:

- `healthchecker.go` — starts/stops pingers per remote endpoint, updates
  connection status on gateway transitions

---

## Event Handler Pattern (how to add a new handler)

1. Create a new package under `pkg/routeagent_driver/handlers/<name>/`
2. Define a struct that embeds `event.HandlerBase` (and optionally
   `event.NodeHandlerBase`)
3. Implement `GetName() string` and `GetNetworkPlugins() []string`
4. Override only the lifecycle methods you need (e.g., `TransitionToGateway`,
   `RemoteEndpointCreated`)
5. Register it in `pkg/routeagent_driver/main.go` by passing handlers into
   `event.NewRegistry(..., handlers...)`

---

## Cable Driver Pattern (how to add a new cable driver)

1. Create a package under `pkg/cable/<name>/`
2. Implement the `cable.Driver` interface (`pkg/cable/driver.go`):
   - `Init(ctx context.Context) error`
   - `GetActiveConnections() ([]v1.Connection, error)`
   - `GetConnections() ([]v1.Connection, error)`
   - `ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error)`
   - `DisconnectFromEndpoint(endpoint *types.SubmarinerEndpoint, family k8snet.IPFamily) error`
   - `GetName() string`
   - `Cleanup(ctx context.Context) error`
3. Register via `cable.AddDriver(name, constructor)` — constructor function signature in `pkg/cable/driver.go`

---

## Packet Filter (`pkg/packetfilter/`)

Abstraction over iptables and nftables:

- `pkg/packetfilter/iptables/` — iptables backend
- `pkg/packetfilter/nftables/` — nftables backend
- `pkg/packetfilter/configure/` — selects backend at startup
- `pkg/packetfilter/fake/` — test fake

Use the `packetfilter.Interface` — do not call iptables/nftables directly.

---

## Go Module Layout

| Module       | Location       | Purpose                                     |
|--------------|----------------|---------------------------------------------|
| Main module  | `go.mod`       | All binary dependencies                     |
| Tools module | `tools/go.mod` | Build/lint tooling only (separate module)   |

When updating deps: check which `go.mod` contains the package before running
`go get`.

---

## Logging

Uses `github.com/submariner-io/admiral/pkg/log` wrapping `controller-runtime`'s
`logf`. Pattern:

```go
var logger = log.Logger{Logger: logf.Log.WithName("component-name")}
logger.Infof("message %v", val)
logger.Errorf(err, "message")
```

Do not use `fmt.Println`, `log.Printf`, or `slog` directly.

---

## Testing

**Quick reference:**

- **Unit tests:** `make unit` (Ginkgo/Gomega, fakes in `fake/` subdirs)
- **E2E tests:** `make e2e` (default: kindnet + IPsec + NON-globalnet)
- **Test structure:** `test/e2e/` (dataplane, cluster, redundancy, compliance)
- **Linting:** `make lint` and `make markdownlint` (required before committing .md files)

---

## Key External Dependencies

| Package                                | Purpose                                              |
|----------------------------------------|------------------------------------------------------|
| `github.com/submariner-io/admiral`     | Shared utilities (logging, syncer, watcher, broker)  |
| `github.com/ovn-org/libovsdb`          | OVN OVSDB client                                     |
| `github.com/kelseyhightower/envconfig` | Config from env vars                                 |
| `sigs.k8s.io/controller-runtime`       | Controller/manager framework                         |
| `k8s.io/client-go`                     | Kubernetes API client                                |

---

## Container Images

Three images are built and shipped. All use a multi-stage Dockerfile:
**builder** (compiles Go binary) → **base** (installs RPM deps) → **scratch**
(final minimal image).

### submariner-gateway

**Dockerfile:** `package/Dockerfile.submariner-gateway`
**Binary built:** `bin/linux/<arch>/submariner-gateway` + `await-node-ready`
**Entrypoint:** `package/submariner.sh`
**RPM deps:** `libreswan`, `iproute`, `iptables`, `kmod`, `openssl`, `nss-tools`

Needs libreswan for IPsec, kmod to load kernel modules.

### submariner-route-agent

**Dockerfile:** `package/Dockerfile.submariner-route-agent`
**Binary built:** `bin/linux/<arch>/submariner-route-agent` + `await-node-ready`
**Entrypoint:** `package/submariner-route-agent.sh`
**RPM deps:** `iproute`, `iptables-legacy`, `iptables-nft`, `nftables`, `ipset`,
`openvswitch`, `procps-ng`, `grep`

Needs openvswitch for OVN/OVS interaction, both iptables variants for legacy
and nft backends, ipset for efficient IP set management.
Installs `iptables-wrappers` to select the right iptables backend at runtime.

### submariner-globalnet

**Dockerfile:** `package/Dockerfile.submariner-globalnet`
**Binary built:** `bin/linux/<arch>/submariner-globalnet`
**Entrypoint:** `package/submariner-globalnet.sh`
**RPM deps:** `iproute`, `iptables-legacy`, `iptables-nft`, `nftables`, `ipset`,
`grep`

Similar to route-agent but no OVS dependency (globalnet doesn't talk to OVN).

---

## Metrics

Exposed via Prometheus on port **32780** (default, configurable via
`SUBMARINER_METRICSPORT` env var). Both gateway and globalnet expose metrics;
route-agent exposes only a pprof profile endpoint.

### Gateway metrics (`pkg/cable/metrics.go`)

| Metric                                       | Type  | Description                                 |
|----------------------------------------------|-------|---------------------------------------------|
| `submariner_gateway_rx_bytes`                | Gauge | Bytes received per tunnel                   |
| `submariner_gateway_tx_bytes`                | Gauge | Bytes transmitted per tunnel                |
| `submariner_connections`                     | Gauge | Connection count + status (full labels)     |
| `submariner_connections_short`               | Gauge | Connection count + status (no cable labels) |
| `submariner_connection_established_timestamp`| Gauge | Timestamp of last successful connection     |
| `submariner_connection_latency_seconds`      | Gauge | Last RTT per tunnel                         |

Labels: `cable_driver`, `local_cluster`, `local_hostname`, `local_endpoint_ip`,
`remote_cluster`, `remote_hostname`, `remote_endpoint_ip`, `status`.

### Globalnet metrics (`pkg/globalnet/metrics/metrics.go`)

| Metric                                            | Type  | Description                       |
|---------------------------------------------------|-------|-----------------------------------|
| `submariner_global_IP_availability`               | Gauge | Available global IPs per CIDR     |
| `submariner_global_IP_allocated`                  | Gauge | Allocated global IPs per CIDR     |
| `submariner_global_egress_IP_allocated`           | Gauge | Egress IPs allocated per CIDR     |
| `submariner_cluster_global_egress_IP_allocated`   | Gauge | Cluster egress IPs per CIDR       |
| `submariner_global_ingress_IP_allocated`          | Gauge | Ingress IPs allocated per CIDR    |

Label: `cidr`.

---

## CI / Build

```bash
# Build all binaries (all components)
make build

# Build without UPX compression (required for CVE scanning with grype)
make BUILD_UPX=false build

# Build a specific binary only
make bin/linux/amd64/submariner-gateway
make bin/linux/amd64/submariner-route-agent
make bin/linux/amd64/submariner-globalnet

# Build container images (all three)
make images

# Run unit tests
make unit

# Run linters
make lint

# Remove build artifacts
make clean
```

**Binary output:** `bin/linux/<arch>/<binary-name>`
**Supported arches:** `amd64` (default), override with `ARCHES=arm64`

Dockerfiles: `package/Dockerfile.submariner-<component>`
Konflux CI: `.tekton/` pipelines, `package/Dockerfile.submariner-<component>.konflux`
