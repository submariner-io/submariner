FROM fedora:30

WORKDIR /var/submariner

RUN dnf -y distrosync && \
    dnf -y install iproute iptables strongswan procps-ng && \
    dnf -y clean all

COPY submariner.sh submariner-engine /usr/local/bin/

ENTRYPOINT submariner.sh
