FROM registry.fedoraproject.org/fedora-minimal:32

WORKDIR /var/submariner

# iproute and iptables are used internally
# libreswan or strongswan provide IKE
# procps-ng is needed for sysctl
RUN microdnf -y install --nodocs --setopt=install_weak_deps=0 \
             iproute iptables libreswan strongswan procps-ng && \
    microdnf -y clean all

COPY package/submariner.sh package/pluto bin/submariner-engine /usr/local/bin/

# Wrapper scripts to use iptables from the host when that's available
COPY package/iptables-wrapper.in /usr/sbin/

#bubu
ENTRYPOINT submariner.sh
