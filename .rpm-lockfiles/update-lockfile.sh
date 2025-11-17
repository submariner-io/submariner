#!/bin/bash
#
# Regenerates RPM lockfiles for Konflux hermetic builds.
#
# Usage:
#   # Update all components
#   ./.rpm-lockfile/update-lockfile.sh
#
#   # Update a single component
#   ./.rpm-lockfile/update-lockfile.sh gateway
#
# Authentication (if needed):
#   sudo subscription-manager register --org="YOUR_ORG_ID" --activationkey="YOUR_ACTIVATION_KEY" --force
#   sudo subscription-manager refresh
#
#   After re-registering, update certificate IDs in .repo files:
#     NEW_ID=$(ls /etc/pki/entitlement/*.pem | grep -v key | head -1 | sed 's/.*\///;s/.pem//')
#     find .rpm-lockfiles -name "*.repo" -exec sed -i "s/[0-9]\{19\}/$NEW_ID/g" {} \;
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

# Verify certificate IDs in .repo files match actual certificates
CURRENT_CERT_ID=$(ls /etc/pki/entitlement/*.pem 2>/dev/null | grep -v key | head -1 | xargs basename | sed 's/.pem//')
REPO_CERT_IDS=$(grep -h "sslclientcert.*pem" "${REPO_ROOT}/.rpm-lockfiles"/*/*.repo 2>/dev/null | sed 's/.*\/\([0-9]*\)\.pem/\1/' | sort -u)

if [ -n "$CURRENT_CERT_ID" ] && [ -n "$REPO_CERT_IDS" ]; then
  MISMATCHES=$(echo "$REPO_CERT_IDS" | grep -v "^${CURRENT_CERT_ID}$" || true)
  if [ -n "$MISMATCHES" ]; then
    echo "ERROR: Certificate ID mismatch detected"
    echo "Current certificate: ${CURRENT_CERT_ID}"
    echo "Mismatched IDs in .repo files: ${MISMATCHES}"
    echo ""
    echo "Fix with: NEW_ID=\$(ls /etc/pki/entitlement/*.pem | grep -v key | head -1 | sed 's/.*\///;s/.pem//')"
    echo "          find .rpm-lockfiles -name \"*.repo\" -exec sed -i \"s/[0-9]\\{19\\}/\$NEW_ID/g\" {} \\;"
    exit 1
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
