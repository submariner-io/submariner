#!/bin/bash
set -e -x

trap "exit 1" SIGTERM SIGINT

export CHARON_PID_FILE=/var/run/charon.pid
rm -f ${CHARON_PID_FILE}

if [ "${SUBMARINER_DEBUG}" == "true" ]; then
    DEBUG="-v=9"
else
    DEBUG="-v=4"
fi

mkdir -p /etc/ipsec

sysctl -w net.ipv4.conf.all.send_redirects=0

export PATH=$PATH:/usr/libexec/strongswan

exec submariner-engine ${DEBUG} -alsologtostderr
