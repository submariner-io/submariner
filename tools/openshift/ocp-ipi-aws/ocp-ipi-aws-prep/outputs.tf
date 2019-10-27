output "target_public_subnet" {
  value = data.aws_subnet.target_public_subnet.id
}

output "submariner_security_group" {
  value = aws_security_group.submariner_gw_sg.name
}

output "machine_set_config_file" {
  value = "${local_file.machine_set_config.filename}"
}