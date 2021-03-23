# Get vpc data.
data "aws_vpc" "env_vpc" {
  filter {
    name   = "tag:Name"
    values = ["${var.cluster_id}-vpc"]
  }

  filter {
    name   = "tag:kubernetes.io/cluster/${var.cluster_id}"
    values = ["owned"]
  }
}

# Get the list of public subnets.
data "aws_subnet_ids" "env_vpc_public_subnets" {
  vpc_id = data.aws_vpc.env_vpc.id

  filter {
    name   = "tag:Name"
    values = ["${var.cluster_id}-public-${var.aws_region}*"]
  }

  filter {
    name   = "tag:kubernetes.io/cluster/${var.cluster_id}"
    values = ["owned"]
  }
}

# Pick one of the public subnets as a target for modification.
data "aws_subnet" "target_public_subnet" {
  id = tolist(data.aws_subnet_ids.env_vpc_public_subnets.ids)[0]
}

# Pick another public subnets for HA as a target for modification.
data "aws_subnet" "target_public_subnet_ha" {
  id = tolist(data.aws_subnet_ids.env_vpc_public_subnets.ids)[1]
}

# Add required tags to target subnet.
resource "null_resource" "target_subnet_tags" {
  triggers = {
    build_number = "${timestamp()}"
  }

  provisioner "local-exec" {
    command = "aws --region ${var.aws_region} ec2 create-tags --resources ${data.aws_subnet.target_public_subnet.id} --tags Key=kubernetes.io/role/internal-elb,Value="
  }
}