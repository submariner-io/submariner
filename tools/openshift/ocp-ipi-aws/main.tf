locals {
  cluster_id = "cluster_id"
  aws_region = "aws_region"
}

provider "aws" {
  region = local.aws_region
}

module "ocp-ipi-aws-prep" {
  source     = "./ocp-ipi-aws-prep"
  aws_region = local.aws_region
  cluster_id = local.cluster_id
}