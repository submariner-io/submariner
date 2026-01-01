#!/bin/bash
#
# Regenerates RPM lockfiles for Konflux hermetic builds.
#
# Usage (run from anywhere in repo):
#   .rpm-lockfiles/update-lockfile.sh <branch> [component]
#
# Examples:
#   .rpm-lockfiles/update-lockfile.sh release-0.21              # Update all components
#   .rpm-lockfiles/update-lockfile.sh release-0.21 globalnet    # Update single component
#
# Prerequisites:
#   - Red Hat entitlement certificates in /etc/pki/entitlement/
#   - Registry auth: podman login registry.redhat.io
#

set -euo pipefail

# Change to repo root (allows running from any directory)
cd "$(git rev-parse --show-toplevel)"

# Cleanup on exit
cleanup() {
  [ -n "${ENTITLEMENTS_DIR:-}" ] && rm -rf -- "$ENTITLEMENTS_DIR"
  # Restore .repo files if modified (in case of mid-run failure)
  if [ "${REPO_FILES_MODIFIED:-}" = true ]; then
    git checkout -- '.rpm-lockfiles/*/*.repo' 2>/dev/null || true
  fi
}
trap cleanup EXIT

# Parse arguments
if [ "$#" -lt 1 ]; then
  echo "Usage: $0 <branch> [component]" >&2
  echo "Example: $0 release-0.21 globalnet" >&2
  exit 1
fi

BRANCH=$1
COMPONENT=${2:-}
FIX_BRANCH="update-rpm-lockfiles-${BRANCH#release-}"

# Fetch latest from origin (continue if branch already exists locally)
if ! git fetch origin "${BRANCH}" 2>/dev/null; then
  if ! git rev-parse "origin/${BRANCH}" >/dev/null 2>&1; then
    echo "ERROR: Branch '${BRANCH}' not found (fetch failed and not cached locally)"
    echo "Available release branches:"
    git branch -r | grep 'origin/release-' | sed 's/^/  /'
    exit 1
  fi
fi

# Verify entitlement certificates exist
if ! ls /etc/pki/entitlement/*.pem &> /dev/null; then
  echo "ERROR: No entitlement certificates found in /etc/pki/entitlement/"
  echo "Run: sudo subscription-manager register --org=\"YOUR_ORG_ID\" --activationkey=\"YOUR_ACTIVATION_KEY\" --force"
  echo "Then: sudo subscription-manager refresh"
  exit 1
fi

# Verify registry authentication
if [ ! -s "${HOME}/.docker/config.json" ]; then
  echo "ERROR: Registry credentials not found at ${HOME}/.docker/config.json"
  echo "Run: podman login registry.redhat.io"
  exit 1
fi

# Create fix branch from target branch
echo "--- Creating branch ${FIX_BRANCH} from ${BRANCH} ---"
git checkout -B "${FIX_BRANCH}" "origin/${BRANCH}"

# Check if .rpm-lockfiles exists
if [ ! -d ".rpm-lockfiles" ]; then
  echo "ERROR: ${BRANCH} does not have .rpm-lockfiles directory"
  exit 1
fi

# Copy entitlement certificates to temp dir to avoid SELinux issues
# (Podman with :Z relabels files, which can break the originals in /etc/pki/entitlement)
ENTITLEMENTS_DIR=$(mktemp -d)
cp -r /etc/pki/entitlement/* "${ENTITLEMENTS_DIR}"

# Get current host's cert ID
CURRENT_CERT_ID=$(basename "$(find /etc/pki/entitlement -maxdepth 1 -name '*.pem' ! -name '*-key.pem' 2>/dev/null | head -1)" .pem)

# Temporarily resolve cert paths in .repo files (restored after lockfile generation)
# dnf doesn't support wildcards, so we must resolve to actual cert ID
REPO_FILES_MODIFIED=false
if grep -q '/\*\.pem\|/\*-key\.pem' .rpm-lockfiles/*/*.repo 2>/dev/null; then
  echo "--- Temporarily resolving wildcard cert paths ---"
  find .rpm-lockfiles -name '*.repo' -exec \
    sed -i "s|/\*\.pem|/${CURRENT_CERT_ID}.pem|g; s|/\*-key\.pem|/${CURRENT_CERT_ID}-key.pem|g" {} \;
  REPO_FILES_MODIFIED=true
else
  REPO_CERT_IDS=$(grep -oh '/[0-9]\{10,\}\.pem' .rpm-lockfiles/*/*.repo 2>/dev/null | sed 's|^/||;s|\.pem$||' | sort -u || true)
  if [ -n "$REPO_CERT_IDS" ] && [ "$REPO_CERT_IDS" != "$CURRENT_CERT_ID" ]; then
    echo "--- Temporarily updating cert IDs for this host ---"
    for old_id in $REPO_CERT_IDS; do
      find .rpm-lockfiles -name '*.repo' -exec \
        sed -i "s|/${old_id}\.pem|/${CURRENT_CERT_ID}.pem|g; s|/${old_id}-key\.pem|/${CURRENT_CERT_ID}-key.pem|g" {} \;
    done
    REPO_FILES_MODIFIED=true
  fi
fi

update_component_lockfile() {
  local component=$1
  local lockfile_dir=".rpm-lockfiles/${component}"

  if [ ! -d "${lockfile_dir}" ]; then
    echo "WARNING: Directory for component '${component}' not found, skipping."
    return
  fi

  echo "--- Updating RPM lockfile for ${BRANCH}:${component} ---"

  podman run --rm -v "$(pwd):/workspace:z" \
         -v "${ENTITLEMENTS_DIR}:/etc/pki/entitlement:ro,Z" \
         -v "${HOME}/.docker/config.json:/run/containers/0/auth.json:ro,Z" \
         registry.access.redhat.com/ubi9/ubi:latest \
         /bin/bash -c "
           set -e
           cd \"/workspace/${lockfile_dir}\"
           dnf install -yq python3-pip git skopeo
           pip3 install -q git+https://github.com/konflux-ci/rpm-lockfile-prototype.git
           rpm-lockfile-prototype --allowerasing rpms.in.yaml
         "

  echo "--- Lockfile for ${component} updated successfully. ---"
}

# Update component(s)
if [ -z "$COMPONENT" ]; then
  for component_path in .rpm-lockfiles/*/; do
    if [ -f "${component_path}/rpms.in.yaml" ]; then
      update_component_lockfile "$(basename "${component_path}")"
    fi
  done
else
  update_component_lockfile "$COMPONENT"
fi

echo ""
if git diff --quiet .rpm-lockfiles/*/rpms.lock.yaml 2>/dev/null; then
  echo "=== No lockfile changes detected ==="
else
  echo "=== Done. Review changes with: git diff ==="
  echo ""
  echo "To commit and create PR:"
  echo "  git add .rpm-lockfiles/*/rpms.lock.yaml"
  echo "  git commit -s -m 'Update RPM lockfiles'"
  echo "  git push origin ${FIX_BRANCH}"
  echo "  gh pr create --base ${BRANCH} --head ${FIX_BRANCH}"
fi
