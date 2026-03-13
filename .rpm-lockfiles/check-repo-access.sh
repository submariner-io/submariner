#!/bin/bash
# Quick check of repository access for Submariner RPM dependencies
#
# Usage (run from anywhere in repo):
#   .rpm-lockfiles/check-repo-access.sh
#
# Tests if repos are accessible (subscription entitlements).
# For full package verification, use: verify-packages.sh [branch]
#
# Requires: curl, entitlement certs in /etc/pki/entitlement/

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Colors
R=$(tput setaf 1) G=$(tput setaf 2) B=$(tput setaf 4) N=$(tput sgr0)

# Find entitlement cert
CERT=$(find /etc/pki/entitlement -name "*.pem" ! -name "*-key.pem" 2>/dev/null | head -1)
KEY=${CERT%.pem}-key.pem
[[ -f "$CERT" ]] || { echo "No entitlement certs. Run: sudo subscription-manager register"; exit 1; }

# Check repo access (returns 0=accessible, 1=blocked)
check() {
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" -k --cert "$CERT" --key "$KEY" "$1" 2>/dev/null) && [[ $code -eq 200 ]]
}

check_public() {
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" "$1" 2>/dev/null) && [[ $code -eq 200 ]]
}

# Repo URLs
RHEL="https://cdn.redhat.com/content/dist/rhel10/10"
FDP="https://cdn.redhat.com/content/dist/layered/rhel10"
UBI="https://cdn-ubi.redhat.com/content/public/ubi/dist/ubi10/10"

echo -e "${B}Submariner RPM Dependency Status${N}"
echo "================================="
echo
echo -e "${B}Component    Package       Repository       x86_64  aarch64 ppc64le s390x${N}"
echo "----------   -----------   --------------   ------  ------- ------- -----"

# gateway: libreswan from RHEL 10 AppStream
printf "gateway      libreswan     RHEL 10 AppStream "
for arch in x86_64 aarch64 ppc64le s390x; do
    if check "$RHEL/$arch/appstream/os/repodata/repomd.xml"; then
        printf "${G}%-8s${N}" "OK"
    else
        printf "${R}%-8s${N}" "403"
    fi
done
echo

# route-agent: openvswitch from fast-datapath
printf "route-agent  openvswitch   fast-datapath    "
for arch in x86_64 aarch64 ppc64le s390x; do
    if check "$FDP/$arch/fast-datapath/os/repodata/repomd.xml"; then
        printf "${G}%-8s${N}" "OK"
    else
        printf "${R}%-8s${N}" "403"
    fi
done
echo

# globalnet: iptables-nft from RHEL 10 AppStream
printf "globalnet    iptables-nft  RHEL 10 AppStream "
for arch in x86_64 aarch64 ppc64le s390x; do
    if check "$RHEL/$arch/appstream/os/repodata/repomd.xml"; then
        printf "${G}%-8s${N}" "OK"
    else
        printf "${R}%-8s${N}" "403"
    fi
done
echo

echo
echo -e "${B}Legend:${N} ${G}OK${N}=accessible  ${R}403${N}=subscription lacks this arch"
