restart ?= all
focus ?= .\*
BASE_BRANCH ?= devel
export BASE_BRANCH

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

IMAGES ?= submariner submariner-gateway submariner-route-agent submariner-globalnet submariner-networkplugin-syncer
images: build

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e reload-images)
override BUILD_ARGS += --ldflags '-X main.VERSION=$(VERSION)'

ifneq (,$(filter ovn,$(_using)))
override CLUSTER_SETTINGS_FLAG = --cluster_settings $(DAPPER_SOURCE)/scripts/cluster_settings.ovn
else
override CLUSTER_SETTINGS_FLAG = --cluster_settings $(DAPPER_SOURCE)/scripts/cluster_settings
endif

override CLUSTERS_ARGS += $(CLUSTER_SETTINGS_FLAG)
override DEPLOY_ARGS += $(CLUSTER_SETTINGS_FLAG)
override E2E_ARGS += --focus $(focus) cluster2 cluster3 cluster1
override UNIT_TEST_ARGS += test/e2e
override VALIDATE_ARGS += --skip-dirs pkg/client

# When cross-building, we need to map Go architectures and operating systems to Docker buildx platforms:
# Docker buildx platform | Fedora support? | Go
# --------------------------------------------------
# linux/amd64            | Yes (amd64)     | linux/amd64
# linux/arm64            | Yes (arm64v8)   | linux/arm64
# linux/riscv64          | No              | linux/riscv64
# linux/ppc64le          | Yes (ppc64le)   | linux/ppc64le
# linux/s390x            | Yes (s390x)     | linux/s390x
# linux/386              | No              | linux/386
# linux/arm/v7           | Yes (arm32v7)   | linux/arm
# linux/arm/v6           | No              | N/A
#
# References: https://github.com/golang/go/blob/master/src/go/build/syslist.go
gotodockerarch = $(patsubst arm,arm/v7,$(1))
dockertogoarch = $(patsubst arm/v7,arm,$(1))

# Targets to make

deploy: images

reload-images: build images
	./scripts/$@ --restart $(restart)

bin/%/submariner-gateway: vendor/modules.txt main.go $(shell find pkg -not \( -path 'pkg/globalnet*' -o -path 'pkg/routeagent*' \))
	GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $@ main.go $(BUILD_ARGS)

bin/%/submariner-route-agent: vendor/modules.txt $(shell find pkg/routeagent_driver)
	GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $@ ./pkg/routeagent_driver/main.go $(BUILD_ARGS)

bin/%/submariner-globalnet: vendor/modules.txt $(shell find pkg/globalnet)
	GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $@ ./pkg/globalnet/main.go $(BUILD_ARGS)

bin/%/submariner-networkplugin-syncer: vendor/modules.txt $(shell find pkg/networkplugin-syncer)
	GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $@ ./pkg/networkplugin-syncer/main.go $(BUILD_ARGS)

nullstring :=
space := $(nullstring) # end of the line
comma := ,

# Single-architecture only for now (we need to support manifests in Shipyard)
# This can be overridden to build for other supported architectures; the reference is the Go architecture,
# so "make images ARCHES=arm" will build a linux/arm/v7 image
ARCHES ?= amd64
BINARIES = submariner-gateway submariner-route-agent submariner-globalnet submariner-networkplugin-syncer
IMAGES_ARGS = --platform $(subst $(space),$(comma),$(foreach arch,$(subst $(comma),$(space),$(ARCHES)),linux/$(call gotodockerarch,$(arch))))

build: $(foreach arch,$(subst $(comma),$(space),$(ARCHES)),$(foreach binary,$(BINARIES),bin/linux/$(call gotodockerarch,$(arch))/$(binary)))

ci: validate unit build images

$(TARGETS): vendor/modules.txt
	./scripts/$@

.PHONY: $(TARGETS) build ci images unit validate

else

# Not running in Dapper

Makefile.dapper:
	@echo Downloading $@
	@curl -sfLO https://raw.githubusercontent.com/submariner-io/shipyard/$(BASE_BRANCH)/$@

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.inc: ;
