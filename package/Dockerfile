FROM registry.fedoraproject.org/fedora-minimal:30

WORKDIR /var/submariner

# iproute and iptables are used internally
# libreswan provides IKE
# procps is needed for sysctl
RUN microdnf install iproute iptables libreswan procps

COPY submariner.sh pluto submariner-engine /usr/local/bin/

# temporary sleep infinity so that we can do our debugging
ENTRYPOINT submariner.sh