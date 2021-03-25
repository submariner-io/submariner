locals {
  cluster_id = "cluster_id"
  aws_region = "aws_region"
  ipsec_natt_port = 4500
  ipsec_ike_port = 500
  nat_discovery_port = 4490
  gw_instance_type = "m5n.large"
  enable_ha = false
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
  nat_discovery_port = local.nat_discovery_port
  gw_instance_type = local.gw_instance_type
  enable_ha = local.enable_ha
}
