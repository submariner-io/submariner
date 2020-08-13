# OCP-IPI tool

This set of scripts is designed to update openshift-installer provisioned AWS infrastructure
to host submariner properly. Submariner needs at least one worker node with an external IP
address, and marked with the submariner.io/gateway=true label.

You can use the prep_for_subm.sh script on your openshift install directory with your AWS
credentials correctly sourced.

```bash
cd my-cluster-openshift-install-dir
curl https://raw.githubusercontent.com/submariner-io/submariner/master/tools/openshift/ocp-ipi-aws/prep_for_subm.sh -L -O
chmod a+x ./prep_for_subm.sh
./prep_for_subm.sh
```

## Prerequisites

You will need:

* Terraform: https://www.terraform.io/downloads.html
* AWS CLI tool: https://docs.aws.amazon.com/cli/latest/userguide/cli-chap-install.html
* Unzip
