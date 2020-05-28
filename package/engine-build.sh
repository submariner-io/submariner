#!/bin/bash

function binaries {
    source=$1
    target=$2
    shift 2
    echo COPY --from=builder "${@/#/${source}}" "${target}"
}

function libraries {
    source=$1
    prefix=$2
    target=$3
    shift 3
    set $(ldd "${@/#/${source}}" | awk '/=>/ && !/\/strongswan\// { print $3 }' | sort -u)
    echo COPY --from=builder "${@/#/${prefix}}" "${target}"
}

function directories {
    for directory; do
	echo COPY --from=builder ${directory} ${directory}/
    done
}

cat <<EOF
FROM quay.io/submariner/submariner-engine-builder:latest AS builder

FROM scratch

WORKDIR /var/submariner

# Dynamic linker
COPY --from=builder /usr/lib64/ld-linux* /lib64/
EOF

# Bash, tools we need for the build, tools we need for our scripts
# (we'll fix sh later, but we need it for RUN)
BIN_TOOLS=(bash sh ln mkdir rm)
SBIN_TOOLS=(sysctl)

# Tools we need for the engine
SBIN_TOOLS=("${SBIN_TOOLS[@]}" ip iptables)

# strongSwan
directories /etc/strongswan /usr/lib64/strongswan /usr/libexec/strongswan
SBIN_TOOLS=("${SBIN_TOOLS[@]}" charon-cmd strongswan swanctl)

# Libreswan
echo COPY --from=builder /etc/ipsec.conf /etc/ipsec.secrets /etc/nsswitch.conf /etc/
echo COPY --from=builder /etc/crypto-policies/back-ends/libreswan.config /etc/crypto-policies/back-ends/
directories /etc/ipsec.d /usr/libexec/ipsec /usr/lib64/pkcs11 /usr/share/p11-kit/modules
BIN_TOOLS=("${BIN_TOOLS[@]}" \[ basename certutil grep id pidof sed uname)
SBIN_TOOLS=("${SBIN_TOOLS[@]}" ipsec)
echo COPY --from=builder /usr/lib64/libsoftokn3.so /usr/lib64/libsoftokn3.chk /usr/lib64/libsqlite3.so.0 /usr/lib64/libnss_sss.so.2 /usr/lib64/libnss_files.so.2 /usr/lib64/libfreeblpriv3.so /usr/lib64/libfreeblpriv3.chk /usr/lib64/p11-kit-proxy.so /usr/lib64/libffi.so.6 /usr/lib64/libtasn1.so.6 /lib64/

# Our binaries
echo COPY submariner.sh pluto submariner-engine /usr/local/bin/

# Copy the binaries and libraries
binaries /usr/bin/ /bin/ "${BIN_TOOLS[@]}"
binaries /usr/sbin/ /sbin/ "${SBIN_TOOLS[@]}"
libraries /usr/bin/ /usr /lib64/ "${BIN_TOOLS[@]}"
libraries /usr/sbin/ /usr /lib64/ "${SBIN_TOOLS[@]}"
libraries / /usr /lib64/ /usr/libexec/strongswan/*
libraries / /usr /lib64/ /usr/libexec/ipsec/*

# Directories we need
echo RUN mkdir -p /tmp /run/pluto /run/strongswan /var/log/pluto/peer

# Fix up /bin/sh and /usr symlinks
echo RUN ln -sf bash /bin/sh
echo RUN ln -sf /bin /usr/bin
echo RUN ln -sf /sbin /usr/sbin

echo ENTRYPOINT submariner.sh
