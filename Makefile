restart ?= all
FOCUS ?=
SKIP ?=
PLUGIN ?=
BASE_BRANCH ?= devel
PROTOC_VERSION=3.17.3
export BASE_BRANCH

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

IMAGES ?= submariner-gateway submariner-route-agent submariner-globalnet submariner-networkplugin-syncer
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

override E2E_ARGS += cluster2 cluster1
override UNIT_TEST_ARGS += test
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

golangci-lint: pkg/natdiscovery/proto/natdiscovery.pb.go

unit: pkg/natdiscovery/proto/natdiscovery.pb.go

reload-images: build images
	./scripts/$@ --restart $(restart)

%.pb.go: %.proto bin/protoc-gen-go bin/protoc
	PATH="$(CURDIR)/bin:$$PATH" protoc --go_out=$$(go env GOPATH)/src $<

bin/protoc-gen-go: vendor/modules.txt
	mkdir -p $(@D)
	GOFLAGS="" GOBIN="$(CURDIR)/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@$(shell awk '/google.golang.org\/protobuf/ {print $$2}' go.mod)

bin/protoc:
	curl -LO https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-linux-x86_64.zip
	sha256sum -c scripts/protoc.sha256
	unzip protoc-$(PROTOC_VERSION)-linux-x86_64.zip 'bin/*' 'include/*'
	rm -f protoc-$(PROTOC_VERSION)-linux-x86_64.zip

bin/%/submariner-gateway: vendor/modules.txt main.go $(shell find pkg -not \( -path 'pkg/globalnet*' -o -path 'pkg/routeagent*' \)) pkg/natdiscovery/proto/natdiscovery.pb.go
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
ARCH_BINARIES := $(foreach arch,$(subst $(comma),$(space),$(ARCHES)),$(foreach binary,$(BINARIES),bin/linux/$(call gotodockerarch,$(arch))/$(binary)))
IMAGES_ARGS = --platform $(subst $(space),$(comma),$(foreach arch,$(subst $(comma),$(space),$(ARCHES)),linux/$(call gotodockerarch,$(arch))))

build: $(ARCH_BINARIES)

licensecheck: BUILD_ARGS=--debug
licensecheck: $(ARCH_BINARIES) bin/lichen
	bin/lichen -c .lichen.yaml $(ARCH_BINARIES)

bin/lichen: vendor/modules.txt
	mkdir -p $(@D)
	go build -o $@ github.com/uw-labs/lichen

ci: validate unit build images

$(TARGETS): vendor/modules.txt
	./scripts/$@

.PHONY: $(TARGETS) build ci images unit validate licensecheck

else

# Not running in Dapper

Makefile.dapper:
	@echo Downloading $@
	@curl -sfLO https://raw.githubusercontent.com/submariner-io/shipyard/$(BASE_BRANCH)/$@

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.inc: ;
