# OCP-IPI tool

Submariner Gateway nodes need to be able to accept traffic over UDP ports (4500 and 500 by default) when using IPsec. Submariner also uses
UDP port 4800 to encapsulate traffic from the worker and master nodes to the Gateway nodes. Additionally, the default OpenShift deployment
does not allow assigning an elastic public IP to existing worker nodes, which may be necessary on one end of the IPsec connection.

`prep_for_subm` is a script designed to update your OpenShift installer provisioned AWS infrastructure for Submariner deployments,
handling the requirements specified above.

You can use the `prep_for_subm.sh` script on your OpenShift install directory with your AWS credentials correctly sourced.

```bash
cd my-cluster-openshift-install-dir
curl https://raw.githubusercontent.com/submariner-io/submariner/master/tools/openshift/ocp-ipi-aws/prep_for_subm.sh -L -O
chmod a+x ./prep_for_subm.sh
./prep_for_subm.sh
```

Certain parameters, such as the IPsec UDP ports and AWS instance type for the gateway (default `m5n.large`), can be customized before
running the script. For example:

```bash
export IPSEC_NATT_PORT=4501
export IPSEC_IKE_PORT=501
export GW_INSTANCE_TYPE=m4.xlarge
```

## Prerequisites

You will need:

* [Terraform](https://releases.hashicorp.com/terraform/) version 0.12. Maximum compatible version is 0.12.12
* [AWS CLI tool](https://docs.aws.amazon.com/cli/latest/userguide/cli-chap-install.html)
* [OC](https://cloud.redhat.com/openshift/install/aws/installer-provisioned)
