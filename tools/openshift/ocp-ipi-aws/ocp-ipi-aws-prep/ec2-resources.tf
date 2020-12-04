# Get worker security group.
data "aws_security_group" "worker_sg" {
  vpc_id = data.aws_vpc.env_vpc.id

  filter {
    name   = "tag:kubernetes.io/cluster/${var.cluster_id}"
    values = ["owned"]
  }

  filter {
    name   = "tag:Name"
    values = ["${var.cluster_id}-worker-sg"]
  }
}

# Get security group associated with master nodes.
data "aws_security_group" "master_sg" {
  vpc_id = data.aws_vpc.env_vpc.id

  filter {
    name   = "tag:kubernetes.io/cluster/${var.cluster_id}"
    values = ["owned"]
  }

  filter {
    name   = "tag:Name"
    values = ["${var.cluster_id}-master-sg"]
  }
}


# Add a rule for vxlan traffic for all workers.
resource "aws_security_group_rule" "worker_sg_vxlan_rule" {
  security_group_id        = data.aws_security_group.worker_sg.id
  source_security_group_id = data.aws_security_group.worker_sg.id
  from_port                = 4800
  protocol                 = "udp"
  to_port                  = 4800
  type                     = "ingress"
}

# Add a rule for vxlan traffic from master nodes to worker nodes.
resource "aws_security_group_rule" "master_to_worker_sg_vxlan_rule" {
  security_group_id        = data.aws_security_group.worker_sg.id
  source_security_group_id = data.aws_security_group.master_sg.id
  from_port                = 4800
  protocol                 = "udp"
  to_port                  = 4800
  type                     = "ingress"
}

# Add a rule for vxlan traffic from worker nodes to master nodes.
resource "aws_security_group_rule" "worker_to_master_sg_vxlan_rule" {
  security_group_id        = data.aws_security_group.master_sg.id
  source_security_group_id = data.aws_security_group.worker_sg.id
  from_port                = 4800
  protocol                 = "udp"
  to_port                  = 4800
  type                     = "ingress"
}



# Add a rule for metrics traffic for all workers.
resource "aws_security_group_rule" "worker_sg_metrics_rule" {
  security_group_id        = data.aws_security_group.worker_sg.id
  source_security_group_id = data.aws_security_group.worker_sg.id
  from_port                = 8080
  protocol                 = "tcp"
  to_port                  = 8080
  type                     = "ingress"
}

# Add a rule for metrics traffic from master nodes to worker nodes.
resource "aws_security_group_rule" "master_to_worker_sg_metrics_rule" {
  security_group_id        = data.aws_security_group.worker_sg.id
  source_security_group_id = data.aws_security_group.master_sg.id
  from_port                = 8080
  protocol                 = "tcp"
  to_port                  = 8080
  type                     = "ingress"
}



# Create a submariner gateway security group.
resource "aws_security_group" "submariner_gw_sg" {
  name   = "${var.cluster_id}-submariner-gw-sg"
  vpc_id = data.aws_vpc.env_vpc.id

  ingress {
    from_port   = var.ipsec_natt_port
    protocol    = "UDP"
    to_port     = var.ipsec_natt_port
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = var.ipsec_ike_port
    protocol    = "UDP"
    to_port     = var.ipsec_ike_port
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(map(
    "Name", "${var.cluster_id}-submariner-gw-sg",
  ))
}
