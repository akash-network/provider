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

SHELL := $(BASH_PATH)

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

ifeq (, $(shell which direnv))
$(error "No direnv in $(PATH), consider installing. https://direnv.net")
endif

ifneq (1, $(AKASH_DIRENV_SET))
$(error "no envrc detected. might need to run \"direnv allow\"")
endif

# AKASH_ROOT may not be set if environment does not support/use direnv
# in this case define it manually as well as all required env variables
ifndef AP_ROOT
$(error "AP_ROOT is not set. might need to run \"direnv allow\"")
endif

ifeq (, $(GOTOOLCHAIN))
$(error "GOTOOLCHAIN is not set")
endif

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
SEMVER                       := $(ROOT_DIR)/script/semver.sh

__local_go                   := $(shell GOTOOLCHAIN=local go version | cut -d ' ' -f 3 | sed 's/go*//' | tr -d '\n')
__is_local_go_satisfies      := $(shell $(SEMVER) compare "v$(__local_go)" "v1.20.7"; echo $?)

ifeq (-1, $(__is_local_go_satisfies))
$(error "unsupported local go$(__local_go) version . min required go1.21.0")
endif

GO_MOD                       ?= readonly
export GO                    := GO111MODULE=$(GO111MODULE) go
GO_BUILD                     := $(GO) build -mod=$(GO_MOD)
GO_TEST                      := $(GO) test -mod=$(GO_MOD)
GO_VET                       := $(GO) vet -mod=$(GO_MOD)

ifeq ($(OS),Windows_NT)
	DETECTED_OS := Windows
else
	DETECTED_OS := $(shell sh -c 'uname 2>/dev/null || echo Unknown')
endif

GO_MOD_NAME                  := $(shell go list -m 2>/dev/null)

AKASHD_MODULE                := pkg.akt.dev/node
REPLACED_MODULES             := $(shell go list -mod=readonly -m -f '{{ .Replace }}' all 2>/dev/null | grep -v -x -F "<nil>" | grep "^/")
AKASHD_SRC_IS_LOCAL          ?= $(shell $(ROOT_DIR)/script/is_local_gomod.sh "$(AKASHD_MODULE)")
AKASHD_LOCAL_PATH            := $(shell $(GO) list -mod=readonly -m -f '{{ .Dir }}' "$(AKASHD_MODULE)")
AKASHD_VERSION               := $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' $(AKASHD_MODULE) | cut -c2-)
GRPC_GATEWAY_VERSION         := $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' github.com/grpc-ecosystem/grpc-gateway)
GOLANGCI_LINT_VERSION        ?= v2.3.0
STATIK_VERSION               ?= v0.1.7
GIT_CHGLOG_VERSION           ?= v0.15.1
MOCKERY_PACKAGE_NAME         := github.com/vektra/mockery/v3
MOCKERY_VERSION              ?= $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' $(MOCKERY_PACKAGE_NAME))
K8S_CODEGEN_VERSION          ?= $(shell $(GO) list -mod=readonly -m -f '{{ .Version }}' k8s.io/code-generator)
SEMVER_VERSION               ?= v1.3.0

AKASHD_BUILD_FROM_SRC        := false
ifeq (false,$(AKASHD_SRC_IS_LOCAL))
	AKASHD_BUILD_FROM_SRC := $(shell curl -sL \
	  -H "Accept: application/vnd.github+json" \
	  -H "Authorization: Bearer $${GITHUB_TOKEN}" \
	  -H "X-GitHub-Api-Version: 2022-11-28" \
	  https://api.github.com/repos/akash-network/node/releases/tags/$(AKASHD_VERSION) | jq -e --arg name "akash_darwin_all.zip" '.assets[] | select(.name==$$name)' > /dev/null 2>&1 && echo -n true || echo -n false)
else
	AKASHD_BUILD_FROM_SRC := true
endif

RELEASE_DOCKER_IMAGE         ?= ghcr.io/akash-network/provider

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
K8S_CODEGEN_VERSION_FILE         := $(AP_DEVCACHE_VERSIONS)/k8s-codegen/$(K8S_CODEGEN_VERSION)
GOLANGCI_LINT_VERSION_FILE       := $(AP_DEVCACHE_VERSIONS)/golangci-lint/$(GOLANGCI_LINT_VERSION)
AKASHD_VERSION_FILE              := $(AP_DEVCACHE_VERSIONS)/akash/$(AKASHD_VERSION)
KIND_VERSION_FILE                := $(AP_DEVCACHE_VERSIONS)/kind/$(KIND_VERSION)
SEMVER_VERSION_FILE              := $(AP_DEVCACHE_VERSIONS)/semver/$(SEMVER_VERSION)

STATIK                           := $(AP_DEVCACHE_BIN)/statik
GIT_CHGLOG                       := $(AP_DEVCACHE_BIN)/git-chglog
MOCKERY                          := $(AP_DEVCACHE_BIN)/mockery
K8S_KUBE_CODEGEN_FILE            := kube_codegen.sh
K8S_KUBE_CODEGEN                 := $(AP_DEVCACHE_BIN)/$(K8S_KUBE_CODEGEN_FILE)
K8S_GO_TO_PROTOBUF               := $(AP_DEVCACHE_BIN)/go-to-protobuf
GOLANGCI_LINT                    := $(AP_DEVCACHE_BIN)/golangci-lint
SEMVER                           := $(AP_DEVCACHE_BIN)/semver

include $(AP_ROOT)/make/setup-cache.mk
