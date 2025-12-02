# RPM Lockfiles for Konflux Hermetic Builds

This directory contains RPM lockfiles for Submariner container images built
via Konflux. Lockfiles pin exact package versions and checksums for
reproducible, hermetic builds.

## Directory Structure

```text
.rpm-lockfiles/
├── update-lockfile.sh       # Regenerates lockfiles
├── demo-multiarch-access.sh # Diagnoses repository access issues
├── README.md                # This file
├── gateway/                 # VPN gateway component
│   ├── rpms.in.yaml         # Input: packages to resolve
│   ├── rpms.lock.yaml       # Output: resolved packages with checksums
│   └── submariner-rhel-9.repo
├── route-agent/             # Network routing component
│   └── ...
└── globalnet/               # Overlapping CIDR support
    └── ...
```

## Architecture Support

| Component   | x86_64 | aarch64 | ppc64le | s390x |
|-------------|--------|---------|---------|-------|
| gateway     | Yes    | Yes     | No*     | No*   |
| route-agent | Yes    | Yes     | No*     | No*   |
| globalnet   | Yes    | Yes     | Yes     | Yes   |

*Blocked due to package availability (see [Multi-arch Limitations](#multi-arch-limitations))

## Usage

### Prerequisites

1. **Red Hat subscription** with entitlement certificates:
   ```bash
   sudo subscription-manager register --org="ORG_ID" --activationkey="KEY"
   sudo subscription-manager refresh
   ```

2. **Registry authentication** for Red Hat container images:
   ```bash
   podman login registry.redhat.io
   ```

### Regenerating Lockfiles

```bash
# Update all components
./.rpm-lockfiles/update-lockfile.sh

# Update a single component
./.rpm-lockfiles/update-lockfile.sh gateway
```

The script automatically:

- Detects and updates certificate IDs in .repo files
- Runs rpm-lockfile-prototype in a container
- Generates lockfiles with exact versions and checksums

### Diagnosing Issues

```bash
./.rpm-lockfiles/demo-multiarch-access.sh
```

This script tests repository access across architectures and shows which
repos are accessible with your current subscription.

## Multi-arch Limitations

### Problem

Red Hat Developer Subscription only includes x86_64 and aarch64 architectures.
Accessing ppc64le and s390x repositories returns HTTP 403.

### Affected Packages

| Package     | Component   | RHEL Repo       | ppc64le/s390x |
|-------------|-------------|-----------------|---------------|
| libreswan   | gateway     | RHEL 9 GA       | 403 Forbidden |
| openvswitch | route-agent | fast-datapath   | 403 Forbidden |
| iptables-nft| globalnet   | UBI (public)    | Available     |

### Current Solution

- **globalnet**: Uses only UBI packages (public, no subscription needed)
- **gateway/route-agent**: Limited to x86_64 and aarch64

### Potential Solutions

1. **Enterprise subscription** with ppc64le/s390x entitlements
2. **CentOS Stream 9** repos have libreswan and openvswitch for all arches
   (requires policy approval for use in Red Hat product builds)

## File Format Reference

### rpms.in.yaml

Input file specifying packages to resolve:

```yaml
context:
  containerfile: ../../package/Dockerfile.submariner-gateway.konflux

contentOrigin:
  repofiles:
    - submariner-rhel-9.repo

packages:
  - iproute
  - libreswan

arches:
  - x86_64
  - aarch64
```

### rpms.lock.yaml

Output file with resolved packages:

```yaml
lockfileVersion: 1
lockfileVendor: redhat
arches:
- arch: x86_64
  packages:
  - url: https://cdn.redhat.com/.../libreswan-4.x.rpm
    checksum: sha256:abc123...
    name: libreswan
    evr: 4.x-1.el9
```

## Troubleshooting

### Certificate ID Mismatch

If you see errors about certificate IDs, the script auto-fixes them.
If issues persist, manually update the 19-digit cert ID in .repo files:

```bash
ls /etc/pki/entitlement/*.pem  # Find your cert ID
# Update .repo files with the new ID
```

### Registry Authentication Failed

```bash
podman login registry.redhat.io
# Enter your Red Hat credentials
```

### Subscription Not Found

```bash
sudo subscription-manager status
sudo subscription-manager refresh
```
