status ?= onetime
version ?= 1.14.6
logging ?= false
kubefed ?= false
deploytool ?= operator
globalnet ?= false
build_debug ?= false

TARGETS := $(shell ls scripts | grep -v clusters)

.dapper:
	@echo Downloading dapper
	@curl -sL https://releases.rancher.com/dapper/latest/dapper-`uname -s`-`uname -m` > .dapper.tmp
	@@chmod +x .dapper.tmp
	@./.dapper.tmp -v
	@mv .dapper.tmp .dapper

shell:
	./.dapper -m bind -s

clusters: ci
	./.dapper -m bind $@ --k8s_version $(version) --globalnet $(globalnet)

e2e: clusters

$(TARGETS): .dapper vendor/modules.txt
	DAPPER_ENV="OPERATOR_IMAGE"  ./.dapper -m bind $@ --status $(status) --k8s_version $(version) --logging $(logging) --kubefed $(kubefed) --deploytool $(deploytool) --globalnet $(globalnet) --build_debug $(build_debug)

vendor/modules.txt: .dapper go.mod
ifneq ($(status),clean)
	./.dapper -m bind vendor
endif

.DEFAULT_GOAL := ci

.PHONY: $(TARGETS)

