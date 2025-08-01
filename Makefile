GOBIN                  := $(shell go env GOPATH)/bin
LEDGER_ENABLED         ?= true

include make/init.mk

.DEFAULT_GOAL          := bins

DOCKER_RUN             := docker run --rm -v $(shell pwd):/workspace -w /workspace
GOLANGCI_LINT_RUN      := $(GOLANGCI_LINT) run
LINT                    = $(GOLANGCI_LINT_RUN) ./... --disable-all --deadline=5m --enable

GORELEASER_CONFIG      ?= .goreleaser.yaml

GIT_HEAD_COMMIT_LONG   := $(shell git log -1 --format='%H')
GIT_HEAD_COMMIT_SHORT  := $(shell git rev-parse --short HEAD)
GIT_HEAD_ABBREV        := $(shell git rev-parse --abbrev-ref HEAD)

RELEASE_TAG            ?= $(shell git describe --tags --abbrev=0)
IS_PREREL              := $(shell $(ROOT_DIR)/script/is_prerelease.sh "$(RELEASE_TAG)" && echo "true" || echo "false")

GO_LINKMODE            ?= external
BUILD_TAGS             ?= osusergo,netgo,static_build
GORELEASER_STRIP_FLAGS ?=

GO_LINKMODE            ?= external

ifeq ($(LEDGER_ENABLED),true)
	BUILD_TAGS := $(BUILD_TAGS),ledger
endif

GORELEASER_BUILD_VARS := \
-X github.com/akash-network/provider/version.Name=provider-services \
-X github.com/akash-network/provider/version.AppName=provider-services \
-X github.com/akash-network/provider/version.BuildTags=\"$(BUILD_TAGS)\" \
-X github.com/akash-network/provider/version.Version=$(RELEASE_TAG) \
-X github.com/akash-network/provider/version.Commit=$(GIT_HEAD_COMMIT_LONG)

ldflags = -linkmode=$(GO_LINKMODE) \
-X github.com/akash-network/provider/version.Name=provider-services \
-X github.com/akash-network/provider/version.AppName=provider-services \
-X github.com/akash-network/provider/version.BuildTags="$(BUILD_TAGS)" \
-X github.com/akash-network/provider/version.Version=$(shell git describe --tags) \
-X github.com/akash-network/provider/version.Commit=$(GIT_HEAD_COMMIT_LONG)

# check for nostrip option
ifeq (,$(findstring nostrip,$(BUILD_OPTIONS)))
	ldflags                += -s -w
	GORELEASER_STRIP_FLAGS += -s -w
endif

ifeq ($(UNAME_OS_LOWER), darwin)
	ldflags += -extldflags=-Wl,-ld_classic
endif

ldflags += $(LDFLAGS)
ldflags := $(strip $(ldflags))

BUILD_FLAGS := -mod=$(GO_MOD) -tags='$(BUILD_TAGS)' -ldflags='$(ldflags)'

include make/releasing.mk
include make/mod.mk
include make/lint.mk
include make/test-integration.mk
include make/codegen.mk
# tools.mk and changelog.mk are already included above
