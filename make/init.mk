ifeq (, $(shell which direnv))
$(warning "No direnv in $(PATH), consider installing. https://direnv.net")
endif

# expecting GNU make >= 4.0. so comparing major version only
MAKE_MAJOR_VERSION        := $(shell echo $(MAKE_VERSION) | cut -d"." -f1)

ifneq (true, $(shell [ $(MAKE_MAJOR_VERSION) -ge 4 ] && echo true))
$(error "make version is outdated. min required 4.0")
endif

# AP_ROOT may not be set if environment does not support/use direnv
# in this case define it manually as well as all required env variables
ifndef AP_ROOT
	AP_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../)

	include $(AP_ROOT)/.env

	PROVIDER_SERVICES   ?= $(AP_DEVCACHE_BIN)/provider-services
	AKASH               ?= $(AP_DEVCACHE_BIN)/akash

	# setup .cache bins first in paths to have precedence over already installed same tools for system wide use
	PATH         := $(AP_DEVCACHE_BIN):$(AP_DEVCACHE_NODE_BIN):$(PATH)
endif

#vr = $(shell which docker-credential-desktop)
#
#$(error $(vr))

UNAME_OS                   := $(shell uname -s)
UNAME_OS_LOWER             := $(shell uname -s | tr '[:upper:]' '[:lower:]')
# uname reports x86_64. rename to amd64 to make it usable by goreleaser
UNAME_ARCH                 := $(shell uname -m | sed "s/x86_64/amd64/g")

ifeq (, $(shell which wget))
$(error "No wget in $(PATH), consider installing")
endif

ifeq (, $(shell which realpath))
$(error "No realpath in $(PATH), consider installing")
endif

BINS                         := $(PROVIDER_SERVICES) akash

export GO                    := GO111MODULE=$(GO111MODULE) go

GO_MOD_NAME                  := $(shell go list -m 2>/dev/null)
AKASH_SRC_IS_LOCAL           := $(shell $(ROOT_DIR)/script/is_local_gomod.sh "github.com/ovrclk/akash")
AKASH_LOCAL_PATH             := $(shell $(GO) list -mod=readonly -m -f '{{ .Replace }}' "github.com/ovrclk/akash")
AKASH_VERSION                := $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' github.com/ovrclk/akash | cut -c2-)
GRPC_GATEWAY_VERSION         := $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' github.com/grpc-ecosystem/grpc-gateway)
GOLANGCI_LINT_VERSION        ?= v1.50.0
GOLANG_VERSION               ?= 1.16.1
STATIK_VERSION               ?= v0.1.7
GIT_CHGLOG_VERSION           ?= v0.15.1
MODVENDOR_VERSION            ?= v0.3.0
MOCKERY_VERSION              ?= 2.12.1
K8S_CODE_GEN_VERSION         ?= v0.19.3

ifeq (0, $(shell which kind &>/dev/null; echo $$?))
	KIND_VERSION                 ?= $(shell kind --version | cut -d" " -f3)
	KIND                         := $(shell which kind)
	_SYSTEM_KIND                 := true
else
	KIND_VERSION                 ?= $(shell go list -mod=readonly -m -f '{{ .Version }}' sigs.k8s.io/kind)
	KIND                         := $(AP_DEVCACHE_BIN)/kind
	_SYSTEM_KIND                 := false
endif

# <TOOL>_VERSION_FILE points to the marker file for the installed version.
# If <TOOL>_VERSION_FILE is changed, the binary will be re-downloaded.
STATIK_VERSION_FILE              := $(AP_DEVCACHE_VERSIONS)/statik/$(STATIK_VERSION)
MODVENDOR_VERSION_FILE           := $(AP_DEVCACHE_VERSIONS)/modvendor/$(MODVENDOR_VERSION)
GIT_CHGLOG_VERSION_FILE          := $(AP_DEVCACHE_VERSIONS)/git-chglog/$(GIT_CHGLOG_VERSION)
MOCKERY_VERSION_FILE             := $(AP_DEVCACHE_VERSIONS)/mockery/v$(MOCKERY_VERSION)
K8S_CODE_GEN_VERSION_FILE        := $(AP_DEVCACHE_VERSIONS)/k8s-codegen/$(K8S_CODE_GEN_VERSION)
GOLANGCI_LINT_VERSION_FILE       := $(AP_DEVCACHE_VERSIONS)/golangci-lint/$(GOLANGCI_LINT_VERSION)
AKASH_VERSION_FILE               := $(AP_DEVCACHE_VERSIONS)/akash/$(AKASH_VERSION)
KIND_VERSION_FILE                := $(AP_DEVCACHE_VERSIONS)/kind/$(KIND_VERSION)

MODVENDOR                         = $(AP_DEVCACHE_BIN)/modvendor
SWAGGER_COMBINE                   = $(AP_DEVCACHE_NODE_BIN)/swagger-combine
STATIK                           := $(AP_DEVCACHE_BIN)/statik
GIT_CHGLOG                       := $(AP_DEVCACHE_BIN)/git-chglog
MOCKERY                          := $(AP_DEVCACHE_BIN)/mockery
K8S_GENERATE_GROUPS              := $(AP_ROOT)/vendor/k8s.io/code-generator/generate-groups.sh
K8S_GO_TO_PROTOBUF               := $(AP_DEVCACHE_BIN)/go-to-protobuf
NPM                              := npm
GOLANGCI_LINT                    := $(AP_DEVCACHE_BIN)/golangci-lint

AKASH_BIND_LOCAL ?=

# if go.mod contains replace for akash on local filesystem
# bind this path to goreleaser
ifeq ($(AKASH_SRC_IS_LOCAL), true)
AKASH_BIND_LOCAL := -v $(AKASH_LOCAL_PATH):$(AKASH_LOCAL_PATH)
endif

include $(AP_ROOT)/make/setup-cache.mk
