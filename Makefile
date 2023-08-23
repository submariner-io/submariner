BASE_BRANCH ?= devel
export BASE_BRANCH
PROTOC_VERSION=3.17.3

# Running in Dapper
ifneq (,$(DAPPER_HOST_ARCH))
IMAGES ?= submariner-gateway submariner-route-agent submariner-globalnet
MULTIARCH_IMAGES ?= $(IMAGES)
PLATFORMS ?= linux/amd64,linux/arm64
RESTART ?= all

ifneq (,$(filter ovn,$(USING)))
SETTINGS ?= $(DAPPER_SOURCE)/.shipyard.e2e.ovn.yml
else
SETTINGS ?= $(DAPPER_SOURCE)/.shipyard.e2e.yml
endif

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e /)
GIT_COMMIT_HASH := $(shell git show -s --format='format:%H')
GIT_COMMIT_DATE := $(shell git show -s --format='format:%cI')
VERSIONS_MODULE := github.com/submariner-io/submariner/pkg/versions
export LDFLAGS = -X $(VERSIONS_MODULE).version=$(VERSION) \
                 -X $(VERSIONS_MODULE).gitCommitHash=$(GIT_COMMIT_HASH) \
                 -X $(VERSIONS_MODULE).gitCommitDate=$(GIT_COMMIT_DATE)
ifneq (,$(filter external-net,$(_using)))
export TESTDIR = test/external
override export PLUGIN = scripts/e2e/external/hook
endif

# When cross-building, we need to map Go architectures and operating systems to Docker buildx platforms:
# Docker buildx platform | Fedora support? | Go
# --------------------------------------------------
# linux/amd64            | Yes (x86_64)    | linux/amd64
# linux/arm64            | Yes (aarch64)   | linux/arm64
# linux/riscv64          | No              | linux/riscv64
# linux/ppc64le          | Yes (ppc64le)   | linux/ppc64le
# linux/s390x            | Yes (s390x)     | linux/s390x
# linux/386              | No              | linux/386
# linux/arm/v7           | Yes (armv7hl)   | linux/arm
# linux/arm/v6           | No              | N/A
#
# References: https://github.com/golang/go/blob/master/src/go/build/syslist.go
gotodockerarch = $(patsubst arm,arm/v7,$(1))
dockertogoarch = $(patsubst arm/v7,arm,$(1))

# Targets to make

deploy: images

golangci-lint: pkg/natdiscovery/proto/natdiscovery.pb.go

unit: pkg/natdiscovery/proto/natdiscovery.pb.go

%.pb.go: %.proto bin/protoc-gen-go bin/protoc
	PATH="$(CURDIR)/bin:$$PATH" protoc --go_out=$$(go env GOPATH)/src $<

bin/protoc-gen-go:
	mkdir -p $(@D)
	GOFLAGS="" GOBIN="$(CURDIR)/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@$(shell awk '/google.golang.org\/protobuf/ {print $$2}' go.mod)

bin/protoc:
	curl -LO https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-linux-x86_64.zip
	sha256sum -c scripts/protoc.sha256
	unzip protoc-$(PROTOC_VERSION)-linux-x86_64.zip 'bin/*' 'include/*'
	rm -f protoc-$(PROTOC_VERSION)-linux-x86_64.zip

bin/%/submariner-gateway: main.go $(shell find pkg -not \( -path 'pkg/globalnet*' -o -path 'pkg/routeagent*' \)) pkg/natdiscovery/proto/natdiscovery.pb.go
	GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $@ .

bin/%/submariner-route-agent: $(shell find pkg/routeagent_driver)
	GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $@ ./pkg/routeagent_driver

bin/%/submariner-globalnet: $(shell find pkg/globalnet)
	GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $@ ./pkg/globalnet


nullstring :=
space := $(nullstring) # end of the line
comma := ,

# Single-architecture only for now (we need to support manifests in Shipyard)
# This can be overridden to build for other supported architectures; the reference is the Go architecture,
# so "make images ARCHES=arm" will build a linux/arm/v7 image
ARCHES ?= amd64
BINARIES = submariner-gateway submariner-route-agent submariner-globalnet
ARCH_BINARIES := $(foreach arch,$(subst $(comma),$(space),$(ARCHES)),$(foreach binary,$(BINARIES),bin/linux/$(call gotodockerarch,$(arch))/$(binary)))

build: $(ARCH_BINARIES)

licensecheck: export BUILD_DEBUG = true
licensecheck: $(ARCH_BINARIES) bin/lichen
	bin/lichen -c .lichen.yaml $(ARCH_BINARIES)

bin/lichen:
	mkdir -p $(@D)
	go build -o $@ github.com/uw-labs/lichen

$(TARGETS):
	./scripts/$@

.PHONY: $(TARGETS) build licensecheck

# Not running in Dapper
else
Makefile.dapper:
	@echo Downloading $@
	@curl -sfLO https://raw.githubusercontent.com/submariner-io/shipyard/$(BASE_BRANCH)/$@

include Makefile.dapper
endif

# Disable rebuilding Makefile
Makefile Makefile.inc: ;
