#!/bin/bash
# Multi-arch Repository Access Demo
#
# Diagnoses why ppc64le/s390x builds may fail and shows alternatives.
#
# Problem: Red Hat Developer Subscription only includes x86_64/aarch64.
# The ppc64le/s390x architectures require enterprise subscriptions.
#
# Solution paths:
#   1. globalnet: Works on all arches (uses public UBI repos only)
#   2. gateway/route-agent: Could use CentOS Stream 9 (policy approval needed)
#
# This script tests repository access to help diagnose issues.

set -euo pipefail

RED='\033[31m' GRN='\033[32m' YEL='\033[33m' RST='\033[0m'

CERT=
for f in /etc/pki/entitlement/*.pem; do
    [[ -f "$f" && "$f" != *-key.pem ]] && { CERT="$f"; break; }
done
[[ -f "$CERT" ]] || { echo "ERROR: No entitlement certs. Run: sudo subscription-manager register"; exit 1; }
KEY=${CERT%.pem}-key.pem

ok() { echo -e "${GRN}$1${RST}"; }
fail() { echo -e "${RED}$1${RST}"; }
warn() { echo -e "${YEL}$1${RST}"; }

repo() { # url [noauth]
    local args=(-s -o /dev/null -w "%{http_code}") code
    [[ ${2:-} != noauth ]] && args+=(-k --cert "$CERT" --key "$KEY")
    code=$(curl "${args[@]}" "$1" 2>/dev/null) || code=000
    if [[ $code == 200 ]]; then ok "$code"; else fail "$code"; fi
}

echo "Multi-arch Repository Access Demo"
echo "================================="
echo

CDN=https://cdn.redhat.com/content
UBI=https://cdn-ubi.redhat.com/content/public/ubi
CS9=https://mirror.stream.centos.org/9-stream

echo "Red Hat Repository Access (requires subscription):"
printf "  %-14s %-8s %-8s %-8s %-8s\n" "" "x86_64" "aarch64" "ppc64le" "s390x"
printf "  %-14s %-8b %-8b %-8b %-8b\n" "RHEL 9 GA" \
    "$(repo "$CDN/dist/rhel9/9/x86_64/baseos/os/repodata/repomd.xml")" \
    "$(repo "$CDN/dist/rhel9/9/aarch64/baseos/os/repodata/repomd.xml")" \
    "$(repo "$CDN/dist/rhel9/9/ppc64le/baseos/os/repodata/repomd.xml")" \
    "$(repo "$CDN/dist/rhel9/9/s390x/baseos/os/repodata/repomd.xml")"
printf "  %-14s %-8b %-8b %-8b %-8b\n" "fast-datapath" \
    "$(repo "$CDN/dist/layered/rhel9/x86_64/fast-datapath/os/repodata/repomd.xml")" \
    "$(repo "$CDN/dist/layered/rhel9/aarch64/fast-datapath/os/repodata/repomd.xml")" \
    "$(repo "$CDN/dist/layered/rhel9/ppc64le/fast-datapath/os/repodata/repomd.xml")" \
    "$(repo "$CDN/dist/layered/rhel9/s390x/fast-datapath/os/repodata/repomd.xml")"

echo
echo "Public Repository Access (no subscription needed):"
printf "  %-14s %-8s %-8s %-8s %-8s\n" "" "x86_64" "aarch64" "ppc64le" "s390x"
printf "  %-14s %-8b %-8b %-8b %-8b\n" "UBI 9" \
    "$(repo "$UBI/dist/ubi9/9/x86_64/baseos/os/repodata/repomd.xml" noauth)" \
    "$(repo "$UBI/dist/ubi9/9/aarch64/baseos/os/repodata/repomd.xml" noauth)" \
    "$(repo "$UBI/dist/ubi9/9/ppc64le/baseos/os/repodata/repomd.xml" noauth)" \
    "$(repo "$UBI/dist/ubi9/9/s390x/baseos/os/repodata/repomd.xml" noauth)"
printf "  %-14s %-8b %-8b %-8b %-8b\n" "CentOS Stream" \
    "$(repo "$CS9/BaseOS/x86_64/os/repodata/repomd.xml" noauth)" \
    "$(repo "$CS9/BaseOS/aarch64/os/repodata/repomd.xml" noauth)" \
    "$(repo "$CS9/BaseOS/ppc64le/os/repodata/repomd.xml" noauth)" \
    "$(repo "$CS9/BaseOS/s390x/os/repodata/repomd.xml" noauth)"

echo
echo "Submariner Component Status:"
echo -e "  gateway:     $(fail BLOCKED) on ppc64le/s390x - needs libreswan"
echo -e "  route-agent: $(fail BLOCKED) on ppc64le/s390x - needs openvswitch"
echo -e "  globalnet:   $(ok WORKS) on all arches - UBI packages sufficient"

echo
echo "Key Findings:"
echo "  - RHEL 9 GA repos return 403 for ppc64le/s390x (Developer Sub limitation)"
echo "  - fast-datapath returns 403 for ppc64le/s390x (same limitation)"
echo "  - UBI repos work for all arches but lack libreswan/openvswitch"
echo -e "  - $(warn "CentOS Stream 9 has libreswan + openvswitch for all arches!")"

echo
echo "To unblock gateway/route-agent on ppc64le/s390x:"
echo "  Option 1: Enterprise subscription with ppc64le/s390x entitlements"
echo "  Option 2: Use CentOS Stream 9 repos (needs policy approval)"
