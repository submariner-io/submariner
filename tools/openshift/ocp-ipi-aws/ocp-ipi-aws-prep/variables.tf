variable "cluster_id" {
  description = "infraID from metadata json."
}

variable "aws_region" {
  description = "AWS region were the cluster was created."
}

variable "ipsec_ike_port" {
  description = "IPSEC IKE port, normally port 500"
}

variable "ipsec_natt_port" {
  description = "IPSEC NATT and encapsulation port, normally 4500"
}

variable "gw_instance_type" {
  description = "The gateway instance type, normally m5n.large"
}

variable "enable_ha" {
  description = "If set to true gateway HA is enabled, disabled by default"
}
