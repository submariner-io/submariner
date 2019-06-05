FROM ubuntu:18.04

WORKDIR /var/submariner

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get -y update && \
    apt-get -y install iproute2 iptables libatm1 libgmp10

COPY submariner-route-agent.sh /usr/local/bin

RUN chmod +x /usr/local/bin/submariner-route-agent.sh

COPY submariner-route-agent /usr/local/bin

# temporary sleep infinity so that we can do our debugging
ENTRYPOINT submariner-route-agent.sh