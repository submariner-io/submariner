# RPM Lockfiles for Konflux Hermetic Builds

This directory contains RPM lockfiles and tooling for Konflux hermetic container builds.

**Directory Structure:**

- Scripts and docs (this README) live on the `devel` branch
- Component configs (`<component>/rpms.in.yaml`, `.repo` files) live on release branches

## Prerequisites

Red Hat entitlement certificates are required to run the lockfile scripts.

### Activation Key Setup

Go to [Red Hat Console](https://console.redhat.com) → RHEL → Inventory → System Configuration →
Activation Keys. Create or modify a key with required repos:

| Architecture | Repos to Add |
|--------------|--------------|
| x86_64 | `fast-datapath-for-rhel-9-x86_64-rpms` |
| aarch64 | `fast-datapath-for-rhel-9-aarch64-rpms` |

**Notes:**

- BaseOS and AppStream are auto-enabled; only additional repos listed above need to be added.
- s390x gateway uses EUS repos configured in `.repo` files (no activation key changes needed).
- ppc64le/s390x fast-datapath repos exist but require different subscriptions (see Blocking Issues).

### Register Your System

Red Hat VPN may be required.

```bash
# If switching keys, unregister and clean first:
sudo subscription-manager unregister
sudo subscription-manager clean

sudo subscription-manager register --org="YOUR_ORG_ID" --activationkey="YOUR_KEY_NAME"
```

### Verify Access

```bash
.rpm-lockfiles/check-repo-access.sh
```

## Current Status

| Component | x86_64 | aarch64 | ppc64le | s390x |
|-----------|--------|---------|---------|-------|
| gateway   | OK     | OK      | 403     | EUS   |
| route-agent | OK   | OK      | 403     | 403   |
| globalnet | OK     | OK      | OK      | OK    |

**Legend:**

- OK = working with standard RHEL 9 repos or UBI (public)
- EUS = working with Extended Update Support repos (see s390x EUS Solution)
- 403 = repos inaccessible with current subscription (see Blocking Issues)

## s390x EUS Solution

Standard RHEL 9 repos return 403 for s390x with self-serve subscriptions. However,
**EUS (Extended Update Support) repos are accessible** and contain all required packages
for the gateway component.

The solution (implemented on release branches for gateway):

1. Add `skip_if_unavailable = 1` to standard repo entries (allows graceful fallback)
2. Add s390x-specific EUS repo entries pointing to `content/eus/rhel9/9.4/s390x/`
3. Add s390x to `rpms.in.yaml` arches and regenerate lockfile
4. Add `linux/s390x` to Tekton pipeline build-platforms

See release branch `.rpm-lockfiles/gateway/` for working implementation.

**Note:** This solution does NOT work for route-agent because openvswitch2.17 (from
fast-datapath repo) is not available in any s390x repo we can access.

## Blocking Issues

### route-agent ppc64le/s390x

Fast-datapath repos for ppc64le/s390x **exist**
(per [Red Hat errata](https://access.redhat.com/errata/RHBA-2024:1971))
but aren't in self-serve activation keys (403).
They require OpenStack, OpenShift, or RHV subscriptions.

### gateway ppc64le

All RHEL 9 repos for ppc64le return 403 with self-serve activation keys:
- Standard repos: 403
- EUS repos: 403
- TUS/E4S/AUS repos: 403

May require OpenShift Platform Plus or enterprise subscription with ppc64le entitlements.

## Component Details

### gateway

| Package | Available In |
|---------|--------------|
| libreswan | RHEL 9 AppStream |
| iproute, kmod, shadow-utils | UBI (public) |

libreswan is **not in UBI** - only RHEL 9 AppStream. s390x uses EUS repos; ppc64le requires a different subscription.

### route-agent

| Package | Available In |
|---------|--------------|
| openvswitch2.17 | fast-datapath (see Blocking Issues for ppc64le/s390x) |
| dnf-plugins-core, iproute, iptables-nft, nftables, ipset, procps-ng, grep | UBI (public) |

### globalnet

All packages in UBI - works for all 4 architectures.

## Verification Scripts

### Quick Access Check

```bash
.rpm-lockfiles/check-repo-access.sh
```

Example output (actual results depend on your subscription):

```text
Component    Package       Repository       x86_64  aarch64 ppc64le s390x
----------   -----------   --------------   ------  ------- ------- -----
gateway      libreswan     RHEL 9 AppStream OK      OK      403     403
route-agent  openvswitch   fast-datapath    OK      OK      403     403
globalnet    iptables-nft  UBI (public)     OK      OK      OK      OK
```

**Note:** gateway s390x shows 403 because the script tests standard repos. The EUS repos
(configured in `.repo` files on release branches) are accessible - see s390x EUS Solution.

### Detailed Package Verification

```bash
.rpm-lockfiles/verify-packages.sh [branch]
```

Example output (actual results depend on your subscription and branch configuration):

```text
gateway (repos: rhel-9-for-appstream-rpms rhel-9-for-baseos-rpms rhel-9-for-s390x-appstream-eus-rpms ...)
  x86_64   OK: iproute@rhel-baseos kmod@rhel-baseos libreswan@rhel-appstream shadow-utils@rhel-baseos
  aarch64  OK: iproute@rhel-baseos kmod@rhel-baseos libreswan@rhel-appstream shadow-utils@rhel-baseos
  ppc64le  NO REPO ACCESS (subscription lacks ppc64le)
  s390x    OK: iproute@rhel-baseos-eus kmod@rhel-baseos-eus libreswan@rhel-appstream-eus ...

route-agent (repos: fast-datapath-for-rhel-9-rpms ubi-9-for-appstream-rpms ubi-9-for-baseos-rpms)
  x86_64   OK: openvswitch2.17@fast-datapath dnf-plugins-core@ubi-baseos ...
  aarch64  OK: openvswitch2.17@fast-datapath dnf-plugins-core@ubi-baseos ...
  ppc64le  NO REPO ACCESS (subscription lacks ppc64le)
  s390x    NO REPO ACCESS (subscription lacks s390x)

globalnet (repos: ubi-9-for-baseos-rpms)
  x86_64   OK: grep@ubi-baseos iproute@ubi-baseos ipset@ubi-baseos iptables-nft@ubi-baseos
               nftables@ubi-baseos shadow-utils@ubi-baseos
  aarch64  OK: grep@ubi-baseos iproute@ubi-baseos ipset@ubi-baseos iptables-nft@ubi-baseos
               nftables@ubi-baseos shadow-utils@ubi-baseos
  ppc64le  OK: grep@ubi-baseos iproute@ubi-baseos ipset@ubi-baseos iptables-nft@ubi-baseos
               nftables@ubi-baseos shadow-utils@ubi-baseos
  s390x    OK: grep@ubi-baseos iproute@ubi-baseos ipset@ubi-baseos iptables-nft@ubi-baseos
               nftables@ubi-baseos shadow-utils@ubi-baseos
```

### Update Lockfiles

```bash
.rpm-lockfiles/update-lockfile.sh <branch> [component]
```

Generates `rpms.lock.yaml` from component configs on the specified branch.

**Additional prerequisite:** `podman login registry.redhat.io`
