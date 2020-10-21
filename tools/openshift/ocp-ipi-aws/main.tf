locals {
  cluster_id = "cluster_id"
  aws_region = "aws_region"
  ipsec_natt_port = 4500
  ipsec_ike_port = 500
  gw_instance_type = "m5n.large"
}

provider "aws" {
  region = local.aws_region
}

module "ocp-ipi-aws-prep" {
  source     = "./ocp-ipi-aws-prep"
  aws_region = local.aws_region
  cluster_id = local.cluster_id
  ipsec_natt_port = local.ipsec_natt_port
  ipsec_ike_port = local.ipsec_ike_port
  gw_instance_type = local.gw_instance_type
}
