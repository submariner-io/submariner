FROM ubuntu:18.04

WORKDIR /var/submariner

COPY strongswan*.tar.gz /var/tmp

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get -y update && \
    apt-get -y install curl iproute2 iptables libatm1 libgmp10

RUN /bin/bash -c "cd / && tar xf /var/tmp/strongswan*.tar.gz && rm -f /var/tmp/strongswan*.tar.gz"

COPY charon.conf /usr/local/etc/strongswan.d/charon.conf
COPY submariner.sh /usr/local/bin

RUN chmod +x /usr/local/bin/submariner.sh

COPY submariner-engine /usr/local/bin

# temporary sleep infinity so that we can do our debugging
ENTRYPOINT submariner.sh