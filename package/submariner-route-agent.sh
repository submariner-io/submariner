#!/bin/bash
set -e -x

trap "exit 1" SIGTERM SIGINT

if [ "${SUBMARINER_DEBUG}" == "true" ]; then
    DEBUG="--debug -v=9"
else
    DEBUG=""
fi

exec submariner-route-agent ${DEBUG} -alsologtostderr