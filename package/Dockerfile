FROM ubuntu:18.04

WORKDIR /var/submariner

RUN apt-get -y update && \
    apt-get -y upgrade && \
    apt-mark auto \* > /dev/null && \
    apt-get -y install iproute2 iptables libatm1 strongswan-charon strongswan-swanctl && \
    apt-get -y autoremove --purge && \
    rm -rf /var/lib/apt/lists

COPY charon.conf /etc/strongswan.d/charon.conf
COPY submariner.sh submariner-engine /usr/local/bin/

ENTRYPOINT submariner.sh
