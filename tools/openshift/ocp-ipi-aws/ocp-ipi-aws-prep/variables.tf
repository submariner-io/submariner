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

variable "nat_discovery_port" {
  description = "UDP port used by submariner for nat discovery"
}

variable "gw_instance_type" {
  description = "The gateway instance type, normally m5n.large"
}

variable "enable_ha" {
  description = "If set to true gateway HA is enabled, disabled by default"
}
