locals {
  machine_set_config_file = "${path.cwd}/submariner-gw-machine-set-${data.aws_subnet.target_public_subnet.availability_zone}.yaml"
}

# Get worker instances.
data "aws_instances" "worker_instances" {

  filter {
    name   = "vpc-id"
    values = ["${data.aws_vpc.env_vpc.id}"]
  }

  filter {
    name   = "tag:Name"
    values = ["${var.cluster_id}-worker*"]
  }

  filter {
    name   = "tag:kubernetes.io/cluster/${var.cluster_id}"
    values = ["owned"]
  }
}

# Get worker instance.
data "aws_instance" "worker_instance" {
  instance_id = data.aws_instances.worker_instances.ids[0]
}

# Create empty machine set config file.
resource "null_resource" "empty_file" {
  provisioner "local-exec" {
    command = "touch ${local.machine_set_config_file}"
  }
}

# Render the template file.
data "template_file" "machine_set_template" {
  template = file("${path.module}/templates/machine-set.yaml")

  vars = {
    az                   = data.aws_subnet.target_public_subnet.availability_zone
    aws_region           = var.aws_region
    cluster_id           = var.cluster_id
    rhos_ami_id          = data.aws_instance.worker_instance.ami
    submariner_sg_name   = aws_security_group.submariner_gw_sg.name
    public_subnet_name   = "${var.cluster_id}-public-${data.aws_subnet.target_public_subnet.availability_zone}"
  }

  depends_on = [
    "null_resource.empty_file",
  ]
}

# Create machine set config file from template.
resource "local_file" "machine_set_config" {
  content  = data.template_file.machine_set_template.rendered
  filename = local.machine_set_config_file

  depends_on = [
    "null_resource.empty_file",
  ]
}