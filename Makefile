GOBIN                  := $(shell go env GOPATH)/bin
LEDGER_ENABLED         ?= true

include make/init.mk

.DEFAULT_GOAL          := bins

NULL  :=
SPACE := $(NULL)
WHITESPACE := $(NULL) $(NULL)
COMMA := ,

DOCKER_RUN             := docker run --rm -v $(shell pwd):/workspace -w /workspace
GOLANGCI_LINT_RUN      := $(GOLANGCI_LINT) run
LINT                    = $(GOLANGCI_LINT_RUN) ./...

GORELEASER_CONFIG      ?= .goreleaser.yaml

GIT_HEAD_COMMIT_LONG   := $(shell git log -1 --format='%H')
GIT_HEAD_COMMIT_SHORT  := $(shell git rev-parse --short HEAD)
GIT_HEAD_ABBREV        := $(shell git rev-parse --abbrev-ref HEAD)

RELEASE_TAG            ?= $(shell git describe --tags --abbrev=0)
IS_PREREL              := $(shell $(ROOT_DIR)/script/is_prerelease.sh "$(RELEASE_TAG)" && echo "true" || echo "false")

GO_LINKMODE            ?= external
GORELEASER_STRIP_FLAGS ?=

GO_LINKMODE            ?= external

BUILD_TAGS             ?= osusergo netgo muslc gcc
BUILD_FLAGS            :=

ifeq ($(LEDGER_ENABLED),true)
	BUILD_TAGS := $(BUILD_TAGS) ledger
endif

ifneq ($(UNAME_OS),Darwin)
BUILD_OPTIONS          ?= static-link
endif

ifneq (,$(findstring cgotrace,$(BUILD_OPTIONS)))
	BUILD_TAGS += cgotrace
endif

build_tags    := $(strip $(BUILD_TAGS))
build_tags_cs := $(subst $(WHITESPACE),$(COMMA),$(build_tags))

ldflags := -X github.com/cosmos/cosmos-sdk/version.Name=provider-services \
-X github.com/cosmos/cosmos-sdk/version.AppName=provider-services \
-X github.com/cosmos/cosmos-sdk/version.BuildTags="$(build_tags_cs)" \
-X github.com/cosmos/cosmos-sdk/version.Version=$(shell git describe --tags | sed 's/^v//') \
-X github.com/cosmos/cosmos-sdk/version.Commit=$(GIT_HEAD_COMMIT_LONG)

GORELEASER_LDFLAGS := $(ldflags)

ldflags += -linkmode=external

ifeq (static-link,$(findstring static-link,$(BUILD_OPTIONS)))
	ldflags += -extldflags "-L$(AP_DEVCACHE_LIB) -lm -Wl,-z,muldefs"
else
	ldflags += -extldflags "-L$(AP_DEVCACHE_LIB)"
endif

# check for nostrip option
ifeq (,$(findstring nostrip,$(BUILD_OPTIONS)))
	ldflags     += -s -w
	BUILD_FLAGS += -trimpath
endif

ifeq (delve,$(findstring delve,$(BUILD_OPTIONS)))
	BUILD_FLAGS += -gcflags "all=-N -l"
endif

ldflags += $(LDFLAGS)
ldflags := $(strip $(ldflags))

GORELEASER_TAGS  := $(BUILD_TAGS)
GORELEASER_FLAGS := $(BUILD_FLAGS) -mod=$(GO_MOD) -tags='$(build_tags)'

BUILD_FLAGS += -mod=$(GO_MOD) -tags='$(build_tags_cs)' -ldflags '$(ldflags)'

include make/releasing.mk
include make/mod.mk
include make/lint.mk
include make/test-integration.mk
include make/codegen.mk
# tools.mk and changelog.mk are already included above
