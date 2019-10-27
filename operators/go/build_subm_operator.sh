#!/bin/bash
set -ex

version=${1:-dev}
push_image=${2:-false}

cd $(dirname $0)

# gen_subm_operator.sh / the operator-sdk does not like vendoring mode we enable by default
export GOFLAGS=""

if [ ! -d submariner-operator ]; then
  ./gen_subm_operator.sh
fi

cd submariner-operator

go mod vendor

operator-sdk build quay.io/submariner/submariner-operator:$version --verbose

if [[ $push_image = true ]]; then
  docker push quay.io/submariner/submariner-operator:$version
else
  echo "Skipping pushing SubM Operator image to Quay"
fi
