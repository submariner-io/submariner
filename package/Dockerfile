FROM fedora:31

WORKDIR /var/submariner

# iproute and iptables are used internally
# libreswan or strongswan provide IKE
# procps-ng is needed for sysctl
RUN dnf -y distrosync --nodocs --setopt=install_weak_deps=False && \
    dnf -y install --nodocs --setopt=install_weak_deps=False \
                   iproute iptables libreswan strongswan procps-ng && \
    dnf -y clean all

COPY submariner.sh pluto submariner-engine /usr/local/bin/

ENTRYPOINT submariner.sh
