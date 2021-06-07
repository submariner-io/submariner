#!/bin/bash
set -e -x

trap "exit 1" SIGTERM SIGINT

export CHARON_PID_FILE=/var/run/charon.pid
rm -f ${CHARON_PID_FILE}

SUBMARINER_VERBOSITY=${SUBMARINER_VERBOSITY:-2}

if [ "${SUBMARINER_DEBUG}" == "true" ]; then
    DEBUG="-v=3"
else
    DEBUG="-v=${SUBMARINER_VERBOSITY}"
fi

[[ "$(cat /proc/sys/net/ipv4/conf/all/send_redirects)" = 0 ]] || echo 0 > /proc/sys/net/ipv4/conf/all/send_redirects

exec submariner-gateway ${DEBUG} -alsologtostderr
