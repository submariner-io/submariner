#!/bin/bash
set -e -x

trap "exit 1" SIGTERM SIGINT

SUBMARINER_VERBOSITY=${SUBMARINER_VERBOSITY:-2}

if [ "${SUBMARINER_DEBUG}" == "true" ]; then
    DEBUG="-v=3"
else
    DEBUG="-v=${SUBMARINER_VERBOSITY}"
fi

exec submariner-route-agent ${DEBUG} -alsologtostderr
