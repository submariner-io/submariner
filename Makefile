status ?= onetime
build_debug ?= false

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e /)
CLUSTERS_ARGS = --cluster_settings $(DAPPER_SOURCE)/scripts/kind-e2e/cluster_settings

clusters: build package

e2e: deploy
	./scripts/kind-e2e/e2e.sh --status $(status) --deploytool $(deploytool)

$(TARGETS): vendor/modules.txt
	./scripts/$@ --build_debug $(build_debug)

vendor/modules.txt: go.mod
ifneq ($(status),clean)
	go mod download
	go mod vendor
endif

.PHONY: $(TARGETS)

else

# Not running in Dapper

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.dapper Makefile.inc: ;
