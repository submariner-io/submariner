status ?= onetime
version ?= 1.14.6
logging ?= false
kubefed ?= false
deploytool ?= operator
globalnet ?= false
build_debug ?= false

TARGETS := $(shell ls scripts)
SCRIPTS_DIR ?= /opt/shipyard/scripts

.dapper:
	@echo Downloading dapper
	@curl -sL https://releases.rancher.com/dapper/latest/dapper-`uname -s`-`uname -m` > .dapper.tmp
	@@chmod +x .dapper.tmp
	@./.dapper.tmp -v
	@mv .dapper.tmp .dapper

shell:
	./.dapper -m bind -s

cleanup: .dapper
	./.dapper -m bind $(SCRIPTS_DIR)/cleanup.sh

clusters: ci
	./.dapper -m bind $(SCRIPTS_DIR)/clusters.sh --k8s_version $(version) --globalnet $(globalnet)

deploy: clusters
	DAPPER_ENV="OPERATOR_IMAGE" ./.dapper -m bind $(SCRIPTS_DIR)/deploy.sh --globalnet $(globalnet) --deploytool $(deploytool)

e2e: deploy

$(TARGETS): .dapper vendor/modules.txt
	./.dapper -m bind $@ --status $(status) --k8s_version $(version) --logging $(logging) --kubefed $(kubefed) --deploytool $(deploytool) --globalnet $(globalnet) --build_debug $(build_debug)

vendor/modules.txt: .dapper go.mod
ifneq ($(status),clean)
	./.dapper -m bind vendor
endif

.DEFAULT_GOAL := ci

.PHONY: $(TARGETS)

