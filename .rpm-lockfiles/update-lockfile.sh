#!/bin/bash
#
# Regenerates RPM lockfiles for Konflux hermetic builds.
#
# Konflux requires hermetic builds where all dependencies are pre-resolved.
# This script uses rpm-lockfile-prototype to resolve RPM packages from
# Red Hat repos and generate lockfiles that pin exact versions/checksums.
#
# Components:
#   gateway     - VPN gateway (libreswan) - x86_64, aarch64 only
#   route-agent - Network routing (openvswitch) - x86_64, aarch64 only
#   globalnet   - Overlapping CIDR support - all architectures
#
# Architecture Support:
#   x86_64, aarch64: Full support (RHEL GA + fast-datapath repos)
#   ppc64le, s390x:  Only globalnet (UBI repos only, no subscription needed)
#
#   Gateway/route-agent blocked on ppc64le/s390x because:
#   - RHEL GA repos return 403 (Red Hat Developer Subscription limitation)
#   - fast-datapath repo returns 403 (same limitation)
#   - Packages exist in CentOS Stream 9 but policy approval needed to use
#
# Usage:
#   ./.rpm-lockfiles/update-lockfile.sh           # Update all components
#   ./.rpm-lockfiles/update-lockfile.sh gateway   # Update single component
#
# Prerequisites:
#   1. Red Hat subscription with entitlement certificates:
#      sudo subscription-manager register --org="ORG_ID" --activationkey="KEY"
#      sudo subscription-manager refresh
#
#   2. Registry authentication for Red Hat container images:
#      podman login registry.redhat.io
#
# Notes:
#   - Certificate IDs in .repo files are auto-updated if they don't match
#   - Run demo-multiarch-access.sh to diagnose repository access issues
#

set -euo pipefail

SCRIPT_PATH=$(realpath "$0")
SCRIPT_DIR=$(dirname "${SCRIPT_PATH}")
REPO_ROOT=$(realpath "${SCRIPT_DIR}/..")

# Verify entitlement certificates exist
if ! ls /etc/pki/entitlement/*.pem &> /dev/null; then
  echo "ERROR: No entitlement certificates found in /etc/pki/entitlement/"
  echo "Run: sudo subscription-manager register --org=\"YOUR_ORG_ID\" --activationkey=\"YOUR_ACTIVATION_KEY\" --force"
  echo "Then: sudo subscription-manager refresh"
  exit 1
fi

# Verify registry authentication
if [ ! -s "${HOME}/.docker/config.json" ]; then
  echo "ERROR: registry credentials not found at ${HOME}/.docker/config.json"
  echo "Run: podman login registry.redhat.io"
  exit 1
fi

# Auto-fix certificate IDs in .repo files if they don't match current certificates
CURRENT_CERT_ID=
for f in /etc/pki/entitlement/*.pem; do
  [[ -f "$f" && "$f" != *-key.pem ]] && { CURRENT_CERT_ID=$(basename "$f" .pem); break; }
done
REPO_CERT_IDS=$(grep -h "sslclientcert.*pem" "${REPO_ROOT}/.rpm-lockfiles"/*/*.repo 2>/dev/null | sed 's/.*\/\([0-9]*\)\.pem/\1/' | sort -u)

if [ -n "$CURRENT_CERT_ID" ] && [ -n "$REPO_CERT_IDS" ]; then
  MISMATCHES=$(echo "$REPO_CERT_IDS" | grep -v "^${CURRENT_CERT_ID}$" || true)
  if [ -n "$MISMATCHES" ]; then
    echo "Updating .repo certificate IDs from ${MISMATCHES} to ${CURRENT_CERT_ID}"
    find "${REPO_ROOT}/.rpm-lockfiles" -name "*.repo" -exec sed -i "s/[0-9]\{19\}/${CURRENT_CERT_ID}/g" {} \;
  fi
fi

# Create a temporary directory for entitlement certificates to avoid SELinux issues.
# The trap command ensures the temporary directory is cleaned up on script exit.
entitlements_dir=$(mktemp -d)
trap 'rm -rf -- "$entitlements_dir"' EXIT

update_component_lockfile() {
  local component=$1
  local lockfile_dir=".rpm-lockfiles/${component}"

  if [ ! -d "${REPO_ROOT}/${lockfile_dir}" ]; then
    echo "Warning: Directory for component '${component}' not found, skipping."
    return
  fi

  echo "--- Updating RPM lockfile for component: ${component} ---"

  # Clear and re-copy certificates for each component to ensure a clean state.
  rm -rf "${entitlements_dir:?}"/*
  cp -r /etc/pki/entitlement/* "${entitlements_dir}"

  podman run --rm -v "${REPO_ROOT}:/workspace:z" \
         -v "${entitlements_dir}:/etc/pki/entitlement:ro,Z" \
         -v "${HOME}/.docker/config.json:/run/containers/0/auth.json:ro,Z" \
         registry.access.redhat.com/ubi9/ubi:latest \
         /bin/bash -c "
           set -x
           cd /workspace/${lockfile_dir}
           dnf install -y python3-pip git skopeo
           pip3 install git+https://github.com/konflux-ci/rpm-lockfile-prototype.git
           rpm-lockfile-prototype --allowerasing rpms.in.yaml
         "

  echo "--- Lockfile for ${component} updated successfully. ---"
}

if [ "$#" -eq 0 ]; then
  echo "--- No component specified, updating all found components. ---"
  for component_path in "${REPO_ROOT}/.rpm-lockfiles"/*/; do
    if [ -f "${component_path}/rpms.in.yaml" ]; then
      component_name=$(basename "${component_path}")
      update_component_lockfile "${component_name}"
    fi
  done
  echo "--- All components updated. ---"
elif [ "$#" -eq 1 ]; then
  update_component_lockfile "$1"
else
  echo "Usage: $0 [component-name]" >&2
  exit 1
fi
