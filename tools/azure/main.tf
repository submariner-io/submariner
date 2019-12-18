
locals {
  sshKey = file("~/.ssh/id_rsa.pub")
  vnetCdir = "10.254.0.0/16"
  worknetSubnetCdir = "10.254.128.0/17"
  serviceCdir = "10.254.0.0/17"
  dnsServiceIp = "10.254.0.10"
  dockerBridgeCdir = "172.17.0.1/16"

}

resource "azurerm_resource_group" "test" {
  name     = "submarinerTestRg"
  location = "westeurope"
}

resource "azurerm_virtual_network" "test" {
  name                = "submarinerTestVnet"
  address_space       = [local.vnetCdir]
  location            = azurerm_resource_group.test.location
  resource_group_name = azurerm_resource_group.test.name
}

resource "azurerm_subnet" "test" {
  name                 = "nodesubnet"
  resource_group_name  = azurerm_resource_group.test.name
  virtual_network_name = azurerm_virtual_network.test.name
  address_prefix       = local.worknetSubnetCdir

  # Providing a NSG here is DEPRECATED, however, if you use the recommended
  # azurerm_subnet_network_security_group_association resource, each subsequent
  # terraform apply will add/remove/add/remove/... the association.
    network_security_group_id = azurerm_network_security_group.test.id

}

resource "azurerm_network_security_group" "test" {
  name                = "agentpool_nsg"
  location            = azurerm_resource_group.test.location
  resource_group_name = azurerm_resource_group.test.name
}

resource "azurerm_network_security_rule" "Udp500InAllow" {
  name                        = "Udp500InAllow"
  network_security_group_name = azurerm_network_security_group.test.name
  resource_group_name         = azurerm_resource_group.test.name

  priority                   = 1500
  access                     = "Allow"
  direction                  = "Inbound"
  protocol                   = "Udp"
  source_address_prefix      = "Internet"
  source_port_range          = "*"
  destination_address_prefix = "*"
  destination_port_range     = "500"

}

resource "azurerm_network_security_rule" "Udp4500InAllow" {
  name                        = "Udp4500InAllow"
  network_security_group_name = azurerm_network_security_group.test.name
  resource_group_name         = azurerm_resource_group.test.name

  priority                   = 1501
  access                     = "Allow"
  direction                  = "Inbound"
  protocol                   = "Udp"
  source_address_prefix      = "Internet"
  source_port_range          = "*"
  destination_address_prefix = "*"
  destination_port_range     = "4500"
}

resource "azurerm_network_security_rule" "sshIn" {
  name                        = "sshIn"
  network_security_group_name = azurerm_network_security_group.test.name
  resource_group_name         = azurerm_resource_group.test.name

  priority                   = 1502
  access                     = "Allow"
  direction                  = "Inbound"
  protocol                   = "Udp"
  source_address_prefix      = "Internet"
  source_port_range          = "*"
  destination_address_prefix = "*"
  destination_port_range     = "22"
}

resource "azurerm_network_security_rule" "allowAllOut" {
  name                        = "allowAllOut"
  network_security_group_name = azurerm_network_security_group.test.name
  resource_group_name         = azurerm_resource_group.test.name

  priority                   = 100
  access                     = "Allow"
  direction                  = "Outbound"
  protocol                   = "*"
  source_address_prefix      = "Internet"
  source_port_range          = "*"
  destination_address_prefix = "*"
  destination_port_range     = "*"
} 

resource "azurerm_subnet_network_security_group_association" "test" {
  subnet_id                 = azurerm_subnet.test.id
  network_security_group_id = azurerm_network_security_group.test.id
}

resource "random_string" "service_principal_password" {
  length  = 32
  special = true
}

resource "azuread_application" "application" {
  name = "submarinertest"
}

resource "azuread_service_principal" "sp" {
  application_id = azuread_application.application.application_id
}

resource "azuread_service_principal_password" "spp" {
  service_principal_id = azuread_service_principal.sp.id
  value                = random_string.service_principal_password.result

  end_date_relative = "1024h"

  # workaround as sugested in https://github.com/terraform-providers/terraform-provider-azuread/issues/4  as the az ad was not working.
  # this seems to be a replication latency issue. If not applied, the K8 cluster won't get randomly provisioned as the SP is not replicated on time. 
  provisioner "local-exec" {
    command = <<EOF
      sleep 120
EOF

  }
}

resource "azurerm_kubernetes_cluster" "test" {
  name                = "submarinercluster"
  location            = azurerm_resource_group.test.location
  resource_group_name = azurerm_resource_group.test.name
  dns_prefix          = "submarinertest"

  linux_profile {
    admin_username = "submariner"

    ssh_key {
      key_data = local.sshKey
    }
  }

  default_node_pool {
    name           = "default"
    node_count     = 3
    vm_size        = "Standard_DS2_v2"
    vnet_subnet_id = azurerm_subnet.test.id
  }

  service_principal {
    client_id     =  azuread_application.application.application_id
    client_secret =  azuread_service_principal_password.spp.value 
  }

  network_profile {
    network_plugin     = "azure"
    network_policy     = "azure"
    docker_bridge_cidr = local.dockerBridgeCdir 
    dns_service_ip     = local.dnsServiceIp
    service_cidr       = local.serviceCdir
    load_balancer_sku  = "standard"
  }
}