restart ?= all
focus ?= .\*

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e build -e images -e reload-images)
override BUILD_ARGS += $(shell source ${SCRIPTS_DIR}/lib/version; echo --ldflags \'-X main.VERSION=$${VERSION}\')
override CLUSTERS_ARGS += --cluster_settings $(DAPPER_SOURCE)/scripts/cluster_settings
override E2E_ARGS += --focus $(focus) cluster2 cluster3 cluster1
override UNIT_TEST_ARGS += test/e2e
override VALIDATE_ARGS += --skip-dirs pkg/client

# Process extra flags from the `using=a,b,c` optional flag

ifneq (,$(filter libreswan,$(_using)))
cable_driver = libreswan
else ifneq (,$(filter wireguard,$(_using)))
cable_driver = wireguard
endif

ifneq (,$(cable_driver))
ifneq (,$(filter helm,$(_using)))
override DEPLOY_ARGS += --deploytool_submariner_args '--set cable-driver=$(cable_driver)'
else
override DEPLOY_ARGS += --deploytool_submariner_args '--cable-driver $(cable_driver)'
endif
endif

# Targets to make

deploy: images

test: unit-test

reload-images: build images
	./scripts/$@ --restart $(restart)

bin/submariner-engine: vendor/modules.txt main.go $(shell find pkg -not \( -path 'pkg/globalnet*' -o -path 'pkg/routeagent*' \))
	${SCRIPTS_DIR}/compile.sh $@ main.go $(BUILD_ARGS)

bin/submariner-route-agent: vendor/modules.txt $(shell find pkg/routeagent)
	${SCRIPTS_DIR}/compile.sh $@ ./pkg/routeagent/main.go $(BUILD_ARGS)

bin/submariner-globalnet: vendor/modules.txt $(shell find pkg/globalnet)
	${SCRIPTS_DIR}/compile.sh $@ ./pkg/globalnet/main.go $(BUILD_ARGS)

build: bin/submariner-engine bin/submariner-route-agent bin/submariner-globalnet

ci: validate test build images

# Dockerfile dependencies are the file and any file copied into it
docker_deps = $(1) $(shell grep COPY $(1) | sed 's/COPY \(.*\) .*/\1/')
define image-pack =
$(SCRIPTS_DIR)/build_image.sh -i $(lastword $(subst ., ,$@)) -f $(firstword $^) $(IMAGES_ARGS)
touch $@
endef

package/.image.submariner: $(call docker_deps,package/Dockerfile)
	$(image-pack)

package/.image.submariner-route-agent: $(call docker_deps,package/Dockerfile.routeagent)
	$(image-pack)

package/.image.submariner-globalnet: $(call docker_deps,package/Dockerfile.globalnet)
	$(image-pack)

images: package/.image.submariner package/.image.submariner-route-agent package/.image.submariner-globalnet

$(TARGETS): vendor/modules.txt
	./scripts/$@

.PHONY: $(TARGETS) build ci images test validate

else

# Not running in Dapper

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.dapper Makefile.inc: ;
