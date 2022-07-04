GOBIN                  := $(shell go env GOPATH)/bin
KIND_APP_IP            ?= $(shell make -sC _run/kube kind-k8s-ip)
KIND_APP_PORT          ?= $(shell make -sC _run/kube app-http-port)
KIND_VARS              ?= KUBE_INGRESS_IP="$(KIND_APP_IP)" KUBE_INGRESS_PORT="$(KIND_APP_PORT)"

include make/init.mk

.DEFAULT_GOAL          := $(PROVIDER)

DOCKER_RUN             := docker run --rm -v $(shell pwd):/workspace -w /workspace
GOLANGCI_LINT_RUN      := $(GOLANGCI_LINT) run
LINT                    = $(GOLANGCI_LINT_RUN) ./... --disable-all --deadline=5m --enable

GORELEASER_CONFIG       = .goreleaser.yaml

GIT_HEAD_COMMIT_LONG   := $(shell git log -1 --format='%H')
GIT_HEAD_COMMIT_SHORT  := $(shell git rev-parse --short HEAD)
GIT_HEAD_ABBREV        := $(shell git rev-parse --abbrev-ref HEAD)

RELEASE_TAG            ?= $(shell git describe --tags --abbrev=0)
IS_PREREL              := $(shell $(ROOT_DIR)/script/is_prerelease.sh "$(RELEASE_TAG)")

GO_LINKMODE            ?= external
GO_MOD                 ?= readonly
BUILD_TAGS             ?= osusergo,netgo,static_build
GORELEASER_STRIP_FLAGS ?=

ldflags += $(LDFLAGS)
ldflags := $(strip $(ldflags))

BUILD_FLAGS := -mod=$(GO_MOD) -tags='$(BUILD_TAGS)' -ldflags '$(ldflags)'

include make/releasing.mk
include make/mod.mk
include make/lint.mk
include make/test-integration.mk
include make/codegen.mk
