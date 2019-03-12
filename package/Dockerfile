FROM ubuntu:16.04

WORKDIR /var/submariner

COPY strongswan*.tar.gz /var/tmp

RUN echo 'debconf debconf/frontend select Noninteractive' | debconf-set-selections && apt-get -y update && apt-get -y install curl iproute2 iptables libatm1 libgmp10 libmnl0 libnfnetlink0 libssl1.0.0

RUN /bin/bash -c "cd / && cp /var/tmp/strongswan*.tar.gz . && tar -zxvf strongswan*.tar.gz && rm strongswan*.tar.gz"

COPY charon.conf /usr/local/etc/strongswan.d/charon.conf
COPY submariner.sh /usr/local/bin

RUN chmod +x /usr/local/bin/submariner.sh

COPY submariner-engine /usr/local/bin

# temporary sleep infinity so that we can do our debugging
ENTRYPOINT submariner.sh