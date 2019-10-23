output "target_public_subnet" {
  value = module.ocp-ipi-aws-prep.target_public_subnet
}

output "submariner_security_group" {
  value = module.ocp-ipi-aws-prep.submariner_security_group
}

output "machine_set_config_file" {
  value = module.ocp-ipi-aws-prep.machine_set_config_file
}
