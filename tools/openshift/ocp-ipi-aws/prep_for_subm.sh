#!/bin/bash
set -e

# Get OCP installer path, or use current directory
OCP_INS_DIR="$(realpath -- ${1:-.})"
METADATA_JSON="${OCP_INS_DIR}/metadata.json"

# Get Terraform apply options, for the rest of args (if given)
if (( $# > 1 )) ; then
  shift
  TERRAFORM_ARGS=("$@") # e.g. -auto-approve -lock-timeout=3m
fi

# Set Github parameters
GITHUB_BRANCH="${GITHUB_BRANCH:-master}"
GITHUB_USERFORK="${GITHUB_USERFORK:-submariner-io}"
GITHUB_ARCHIVE="https://github.com/$GITHUB_USERFORK/submariner/archive/$GITHUB_BRANCH.tar.gz"


# Functions

req_install() {
  if ! command -v $1 >/dev/null 2>&1; then
    echo "$1 is required by this tool, please install $1" >&2
    exit 2
  fi
}

download_ocp_ipi_aws_tool() {
  echo "Downloading $GITHUB_ARCHIVE and extracting 'ocp-ipi-aws' tool"
  wget -O - $GITHUB_ARCHIVE | tar xz --strip=4 "submariner-${GITHUB_BRANCH}/tools/openshift/ocp-ipi-aws"
}

# Check parameters
if [[ ! -d $OCP_INS_DIR ]] || [[ ! -f $METADATA_JSON ]]; then
  echo "Please provide a valid OpenShift installation directory as the first argument." >&2
  echo "Usage:" >&2
  echo "   $0 <ocp-install-path> [optional terraform apply arguments]" >&2
  echo "" >&2
  exit 1
fi

# Check pre-requisites
for cmd in wget terraform oc aws; do
  req_install $cmd
done


# Main

INFRA_ID=$(egrep -o -E '\"infraID\":\"([^\"]*)\"' $METADATA_JSON | cut -d: -f2 | tr -d \")
REGION=$(egrep -o -E '\"region\":\"([^\"]*)\"' $METADATA_JSON | cut -d: -f2 | tr -d \")

echo infraID: $INFRA_ID
echo region: $REGION

if [[ -z "$INFRA_ID" ]]; then
  echo "infraID could not be found in $METADATA_JSON" >&2
  exit 3
fi

if [[ -z "$REGION" ]]; then
  echo "region could not be found in $METADATA_JSON" >&2
  exit 4
fi

if [[ ! -d ocp-ipi-aws-prep ]]; then
  download_ocp_ipi_aws_tool
fi

sed -i "s/\"cluster_id\"/\"$INFRA_ID\"/g" main.tf
sed -i "s/\"aws_region\"/\"$REGION\"/g" main.tf

terraform init
terraform apply "${TERRAFORM_ARGS[@]}"

MACHINESET=$(ls submariner-gw-machine*.yaml)

if [[ -z "$MACHINESET" ]]; then
  echo "machineset yaml file not found, did you apply the terraform changes?" >&2
  exit 5
fi

export KUBECONFIG=$OCP_INS_DIR/auth/kubeconfig
echo ""
echo "Applying machineset changes to deploy gateway node:"
echo "oc --context=admin apply -f $MACHINESET"
oc --context=admin apply -f $MACHINESET || echo "applying $MACHINESET failed, please try manually with oc"

