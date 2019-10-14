#!/bin/sh
set -e
OCP_INS_DIR="$(realpath ${1:-.})"
METADATA_JSON="${OCP_INS_DIR}/metadata.json"
GITHUB_BRANCH="${GITHUB_BRANCH:-master}"
GITHUB_USERFORK="${GITHUB_USERFORK:-submariner-io}"
GITHUB_ZIP="${GITHUB_ZIP:-https://github.com/$GITHUB_USERFORK/submariner/archive/$GITHUB_BRANCH.zip}"

req_install() {
        if ! command -v $1 >/dev/null 2>&1; then
	    echo "$1 is required by this tool, please install $1" >&2
	    exit 1
	fi
}

download_ocp_ipi_aws_tool() {
	TMP=$(mktemp -d)
	IPI_AWS="submariner-${GITHUB_BRANCH}/tools/openshift/ocp-ipi-aws"
	curl -L "${GITHUB_ZIP}" --output $TMP/submariner.zip
	unzip $TMP/submariner.zip "$IPI_AWS/*" -d $TMP
	cp -rfp $TMP/$IPI_AWS .
	rm -rf $TMP
}

# check pre-requisites

for cmd in unzip terraform oc aws; do
	req_install $cmd
done

# check parameters

if [[ ! -d $OCP_INS_DIR ]] || [[ ! -f $METADATA_JSON ]]; then
	echo "please provide a valid openshift installer directory for this script to work"  >&2
	echo "usage:" >&2
	echo "   prep_for_subm.sh ocp4-install-dir" >&2
	echo ""	>&2
	exit 2
fi

cd $OCP_INS_DIR

INFRA_ID=$(egrep -o -E '\"infraID\":\"([^\"]*)\"' metadata.json | cut -d: -f2 | tr -d \")
REGION=$(egrep -o -E '\"region\":\"([^\"]*)\"' metadata.json | cut -d: -f2 | tr -d \")

echo infraID: $INFRA_ID
echo region: $REGION

if [[ "x$INFRA_ID" == "x" ]]; then
       echo "infraID could not be found in metadata.json" >&2
       exit 3
fi 

if [[ "x$REGION" == "x" ]]; then
       echo "region could not be found in metadata.json" >&2
       exit 4       
fi

if [[ ! -d ocp-ipi-aws ]]; then
	download_ocp_ipi_aws_tool
fi

cd ocp-ipi-aws

sed -i "s/\"cluster_id\"/\"$INFRA_ID\"/g" main.tf
sed -i "s/\"aws_region\"/\"$REGION\"/g" main.tf

terraform init
terraform apply

MACHINESET=$(ls submariner-gw-machine*.yaml)

if [[ "x$MACHINESET" == "x" ]]; then
        echo "machineset yaml file not found, did you apply the terraform changes?" >&2
        exit 5
fi

export KUBECONFIG=$OCP_INS_DIR/auth/kubeconfig
echo ""
echo "Applying machineset changes to deploy gateway node:"
echo "oc --context=admin apply -f $MACHINESET"
oc --context=admin apply -f $MACHINESET || echo "applying $MACHINESET failed, please try manually with oc"