restart ?= all
focus ?= .\*

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e build -e images -e reload-images)
override BUILD_ARGS += $(shell source ${SCRIPTS_DIR}/lib/version; echo --ldflags \'-X main.VERSION=$${VERSION}\')
override CLUSTERS_ARGS += --cluster_settings $(DAPPER_SOURCE)/scripts/cluster_settings
override E2E_ARGS += --focus $(focus) cluster2 cluster3 cluster1

clusters: build images

reload-images: build images
	./scripts/$@ --restart $(restart)

bin/submariner-engine: vendor/modules.txt main.go $(shell find pkg -not \( -path 'pkg/globalnet*' -o -path 'pkg/routeagent*' \))
	${SCRIPTS_DIR}/compile.sh $@ main.go $(BUILD_ARGS)

bin/submariner-route-agent: vendor/modules.txt $(shell find pkg/routeagent)
	${SCRIPTS_DIR}/compile.sh $@ ./pkg/routeagent/main.go $(BUILD_ARGS)

bin/submariner-globalnet: vendor/modules.txt $(shell find pkg/globalnet)
	${SCRIPTS_DIR}/compile.sh $@ ./pkg/globalnet/main.go $(BUILD_ARGS)

build: bin/submariner-engine bin/submariner-route-agent bin/submariner-globalnet

images: build
	./scripts/$@ $(images_flags)

$(TARGETS): vendor/modules.txt
	./scripts/$@

vendor/modules.txt: go.mod
	go mod download
	go mod vendor

.PHONY: $(TARGETS) build

else

# Not running in Dapper

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.dapper Makefile.inc: ;
