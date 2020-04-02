status ?= onetime
version ?= 1.14.6
deploytool ?= operator
globalnet ?= false
build_debug ?= false

TARGETS := $(shell ls scripts | grep -v e2e)
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

clusters: build package
	./.dapper -m bind $(SCRIPTS_DIR)/clusters.sh --k8s_version $(version) --globalnet $(globalnet)

deploy: clusters
	DAPPER_ENV="OPERATOR_IMAGE" ./.dapper -m bind $(SCRIPTS_DIR)/deploy.sh --globalnet $(globalnet) --deploytool $(deploytool)

e2e: deploy
	./.dapper -m bind ./scripts/kind-e2e/e2e.sh --status $(status) --deploytool $(deploytool)

$(TARGETS): .dapper vendor/modules.txt
	./.dapper -m bind $@ --build_debug $(build_debug)

vendor/modules.txt: .dapper go.mod
ifneq ($(status),clean)
	./.dapper -m bind vendor
endif

.DEFAULT_GOAL := ci

.PHONY: $(TARGETS)

