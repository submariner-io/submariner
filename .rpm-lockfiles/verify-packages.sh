#!/bin/bash
# Verify actual RPM package availability using dnf repoquery
#
# Usage (run from anywhere in repo):
#   .rpm-lockfiles/verify-packages.sh [branch]
#
# Examples:
#   .rpm-lockfiles/verify-packages.sh                  # Verify current branch
#   .rpm-lockfiles/verify-packages.sh release-0.21    # Verify specific branch
#
# Runs dnf inside a container to check each package exists for each arch.
# This is the definitive test - same repos and tools as Konflux builds.
#
# Requires: podman, entitlement certs in /etc/pki/entitlement/
#
# Note: Cross-arch verification (e.g., checking aarch64 from x86_64) requires
# subscription entitlements that cover those architectures.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

BRANCH=${1:-}
LOCKFILES_DIR=""
CLEANUP_DIR=""

cleanup() {
    [[ -n "$CLEANUP_DIR" ]] && rm -rf "$CLEANUP_DIR"
}
trap cleanup EXIT

# If branch specified, extract .rpm-lockfiles from that branch
if [[ -n "$BRANCH" ]]; then
    echo "Fetching $BRANCH..."
    git fetch origin "$BRANCH" || { echo "Failed to fetch $BRANCH"; exit 1; }

    CLEANUP_DIR=$(mktemp -d)
    LOCKFILES_DIR="$CLEANUP_DIR"

    # Extract .rpm-lockfiles from the branch
    for comp in gateway route-agent globalnet; do
        mkdir -p "$LOCKFILES_DIR/$comp"
        git show "origin/$BRANCH:.rpm-lockfiles/$comp/rpms.in.yaml" > "$LOCKFILES_DIR/$comp/rpms.in.yaml" 2>/dev/null || continue
        git show "origin/$BRANCH:.rpm-lockfiles/$comp/submariner-rhel-9.repo" > "$LOCKFILES_DIR/$comp/submariner-rhel-9.repo" 2>/dev/null || true
    done
    echo "Verifying packages for $BRANCH"
else
    LOCKFILES_DIR=".rpm-lockfiles"
    echo "Verifying packages for current branch"
fi

# Check prerequisites
command -v podman &>/dev/null || { echo "Requires: podman"; exit 1; }

# Get current cert ID
CERT_ID=$(ls /etc/pki/entitlement/*.pem 2>/dev/null | grep -v key | head -1 | xargs -r basename | sed 's/.pem//')
[[ -n "$CERT_ID" ]] || { echo "No entitlement certs in /etc/pki/entitlement/"; exit 1; }

# Copy certs to temp dir (avoids SELinux issues)
CERTS=$(mktemp -d)
CLEANUP_DIR="${CLEANUP_DIR:-$CERTS}"
[[ "$CLEANUP_DIR" != "$CERTS" ]] && trap 'rm -rf "$CLEANUP_DIR" "$CERTS"' EXIT
mkdir -p "$CERTS/entitlement" "$CERTS/rhsm-ca"
cp /etc/pki/entitlement/* "$CERTS/entitlement/" 2>/dev/null || true
cp /etc/rhsm/ca/* "$CERTS/rhsm-ca/" 2>/dev/null || true

echo -e "\033[1mPackage Availability\033[0m"
echo

podman run --rm \
    -v "$LOCKFILES_DIR:/lf:ro,Z" \
    -v "$CERTS/entitlement:/etc/pki/entitlement:ro,Z" \
    -v "$CERTS/rhsm-ca:/etc/rhsm/ca:ro,Z" \
    -e "CERT_ID=$CERT_ID" \
    registry.access.redhat.com/ubi9/ubi:latest bash -c '
        G="\033[32m" R="\033[31m" Y="\033[33m" B="\033[1m" N="\033[0m"

        # Disable subscription-manager plugin (avoids conflicts)
        mkdir -p /etc/dnf/plugins
        echo -e "[main]\nenabled=0" > /etc/dnf/plugins/subscription-manager.conf

        rm -f /etc/yum.repos.d/*.repo 2>/dev/null

        for comp in gateway route-agent globalnet; do
            [[ -f /lf/$comp/rpms.in.yaml ]] || continue

            # Extract repo names (without $basearch) for display
            repos=$(grep "^\[" /lf/$comp/*.repo 2>/dev/null | sed "s/.*\[//; s/\]//; s/-\$basearch//g" |
                    grep -v "debug\|source" | sort -u | tr "\n" " " | sed "s/ $//")
            echo -e "${B}$comp${N} (repos: $repos)"

            # Copy repos and fix cert IDs to match current host
            for repo in /lf/$comp/*.repo; do
                [[ -f "$repo" ]] || continue
                sed "s/[0-9]\{10,\}/$CERT_ID/g; s/\*\.pem/$CERT_ID.pem/g; s/\*-key\.pem/$CERT_ID-key.pem/g" "$repo" > /etc/yum.repos.d/$(basename "$repo")
            done
            dnf clean all &>/dev/null

            pkgs=$(sed -n "/^packages:/,/^[a-z]/p" /lf/$comp/rpms.in.yaml | grep "^ *-" | sed "s/.*- //")
            pkg_display=$(echo $pkgs)  # space-separated for display
            pkg_count=$(echo $pkg_display | wc -w)
            arches="x86_64 aarch64 ppc64le s390x"

            # Query all arches in parallel (packages + bash for access check)
            echo -e "  ${Y}querying...${N}"
            tmpdir=$(mktemp -d)
            for arch in $arches; do
                (
                    # Get pkg@repo format, simplify repo names (remove arch, -rpms suffix)
                    dnf -q repoquery --forcearch="$arch" --queryformat="%{name}@%{repoid}\n" $pkgs 2>/dev/null |
                        sed "s/-for-rhel-9-[^-]*//; s/-for-ubi-9-[^-]*//; s/-9-for-[^-]*//; s/-rpms$//" |
                        sort -u | grep . > "$tmpdir/$arch" || true
                    # Also check bash to detect repo access issues
                    dnf -q repoquery --forcearch="$arch" bash 2>/dev/null | grep -q . && echo "1" > "$tmpdir/${arch}_access" || true
                ) &
            done
            wait
            printf "\033[1A\033[2K"  # clear "querying..." line

            # Process results in order
            for arch in $arches; do
                printf "  %-8s " "$arch"
                set -- $(cat "$tmpdir/$arch" 2>/dev/null)

                if [[ $# -eq pkg_count ]]; then
                    # Show packages with their repos
                    echo -e "${G}OK${N}: $@"
                elif [[ $# -eq 0 ]]; then
                    # Check if repo access issue or packages missing
                    if [[ ! -f "$tmpdir/${arch}_access" ]]; then
                        echo -e "${R}NO REPO ACCESS${N} (subscription lacks $arch)"
                    else
                        echo -e "${R}MISSING${N}: $pkg_display"
                    fi
                else
                    # Partial - find which are missing
                    ok="" missing=""
                    for pkg in $pkgs; do
                        pkg_with_repo=$(printf "%s\n" "$@" | grep "^${pkg}@" || true)
                        if [[ -n "$pkg_with_repo" ]]; then
                            ok+=" $pkg_with_repo"
                        else
                            missing+=" $pkg"
                        fi
                    done
                    echo -e "${Y}PARTIAL${N}"
                    echo -e "      ${G}ok:${N}$ok"
                    echo -e "      ${R}missing:${N}$missing"
                fi
            done
            rm -rf "$tmpdir"

            rm -f /etc/yum.repos.d/*.repo
            echo
        done
    '
