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

set -euo pipefail

SCRIPT_PATH=$(realpath "$0")
SCRIPT_DIR=$(dirname "${SCRIPT_PATH}")
REPO_ROOT=$(realpath "${SCRIPT_DIR}/..")

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
         -v "${XDG_RUNTIME_DIR}/containers/auth.json:/run/containers/0/auth.json:ro" \
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
