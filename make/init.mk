ifeq (, $(shell which direnv))
$(warning "No direnv in $(PATH), consider installing. https://direnv.net")
endif

ifeq ($(OS),Windows_NT)
$(error "Windows is not supported as build host")
endif

# certain targets need to use bash
# detect where bash is installed
# use akash-node-ready target as example
BASH_PATH := $(shell which bash)

# expecting GNU make >= 4.0. so comparing major version only
MAKE_MAJOR_VERSION := $(shell echo $(MAKE_VERSION) | cut -d "." -f1)
ifneq (true, $(shell [ $(MAKE_MAJOR_VERSION) -ge 4 ] && echo true))
$(error "make version is outdated. min required 4.0")
endif

# expecting BASH >= 4.x. so comparing major version only
BASH_MAJOR_VERSION := $(shell $(BASH_PATH) -c 'echo $$BASH_VERSINFO')
ifneq (true, $(shell [ $(BASH_MAJOR_VERSION) -ge 4 ] && echo true))
$(error "bash version $(shell $(BASH_PATH) -c 'echo $$BASH_VERSION') is outdated. min required 4.0")
endif

# AP_ROOT may not be set if environment does not support/use direnv
# in this case define it manually as well as all required env variables
ifndef AP_ROOT
	AP_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../)

	include $(AP_ROOT)/.env

	PROVIDER_SERVICES   ?= $(AP_DEVCACHE_BIN)/provider-services
	AKASH               ?= $(AP_DEVCACHE_BIN)/akash

	# setup .cache bins first in paths to have precedence over already installed same tools for system wide use
	PATH                := $(AP_DEVCACHE_BIN):$(AP_DEVCACHE_NODE_BIN):$(PATH)
endif

-include $(AP_ROOT)/.devenv

UNAME_OS                   := $(shell uname -s)
UNAME_OS_LOWER             := $(shell uname -s | tr '[:upper:]' '[:lower:]')
# uname reports x86_64. rename to amd64 to make it usable by goreleaser
UNAME_ARCH                 ?= $(shell uname -m | sed "s/x86_64/amd64/g")

ifeq (, $(shell which wget))
$(error "No wget in $(PATH), consider installing")
endif

ifeq (, $(shell which realpath))
$(error "No realpath in $(PATH), consider installing")
endif

BINS                         := $(PROVIDER_SERVICES) akash

GOWORK                       ?= on

GO_MOD                       ?= vendor
ifeq ($(GOWORK), on)
GO_MOD                       := readonly
endif

export GO                    := GO111MODULE=$(GO111MODULE) go
GO_BUILD                     := $(GO) build -mod=$(GO_MOD)
GO_TEST                      := $(GO) test -mod=$(GO_MOD)
GO_VET                       := $(GO) vet -mod=$(GO_MOD)

GO_MOD_NAME                  := $(shell go list -m 2>/dev/null)

NODE_MODULE                  := github.com/akash-network/node
REPLACED_MODULES             := $(shell go list -mod=readonly -m -f '{{ .Replace }}' all 2>/dev/null | grep -v -x -F "<nil>" | grep "^/")
AKASH_SRC_IS_LOCAL           := $(shell $(ROOT_DIR)/script/is_local_gomod.sh "$(NODE_MODULE)")
AKASH_LOCAL_PATH             := $(shell $(GO) list -mod=readonly -m -f '{{ .Replace }}' "$(NODE_MODULE)")
AKASH_VERSION                := $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' $(NODE_MODULE) | cut -c2-)
GRPC_GATEWAY_VERSION         := $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' github.com/grpc-ecosystem/grpc-gateway)
GOLANGCI_LINT_VERSION        ?= v1.51.2
GOLANG_VERSION               ?= 1.16.1
STATIK_VERSION               ?= v0.1.7
GIT_CHGLOG_VERSION           ?= v0.15.1
MOCKERY_VERSION              ?= 2.24.0
K8S_CODE_GEN_VERSION         ?= $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' k8s.io/code-generator)

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
GIT_CHGLOG_VERSION_FILE          := $(AP_DEVCACHE_VERSIONS)/git-chglog/$(GIT_CHGLOG_VERSION)
MOCKERY_VERSION_FILE             := $(AP_DEVCACHE_VERSIONS)/mockery/v$(MOCKERY_VERSION)
K8S_CODE_GEN_VERSION_FILE        := $(AP_DEVCACHE_VERSIONS)/k8s-codegen/$(K8S_CODE_GEN_VERSION)
GOLANGCI_LINT_VERSION_FILE       := $(AP_DEVCACHE_VERSIONS)/golangci-lint/$(GOLANGCI_LINT_VERSION)
AKASH_VERSION_FILE               := $(AP_DEVCACHE_VERSIONS)/akash/$(AKASH_VERSION)
KIND_VERSION_FILE                := $(AP_DEVCACHE_VERSIONS)/kind/$(KIND_VERSION)

STATIK                           := $(AP_DEVCACHE_BIN)/statik
GIT_CHGLOG                       := $(AP_DEVCACHE_BIN)/git-chglog
MOCKERY                          := $(AP_DEVCACHE_BIN)/mockery
K8S_KUBE_CODEGEN_FILE            := generate-groups.sh
K8S_KUBE_CODEGEN                 := $(AP_DEVCACHE_BIN)/$(K8S_KUBE_CODEGEN_FILE)
K8S_GO_TO_PROTOBUF               := $(AP_DEVCACHE_BIN)/go-to-protobuf
GOLANGCI_LINT                    := $(AP_DEVCACHE_BIN)/golangci-lint

include $(AP_ROOT)/make/setup-cache.mk
