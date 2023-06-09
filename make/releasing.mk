GORELEASER_RELEASE       ?= false
GORELEASER_DEBUG         ?= false
GORELEASER_IMAGE         := ghcr.io/goreleaser/goreleaser-cross:v$(GOLANG_VERSION)
GORELEASER_MOUNT_CONFIG  ?= false
RELEASE_DOCKER_IMAGE     ?= ghcr.io/akash-network/provider

ifeq ($(GORELEASER_RELEASE),true)
	GORELEASER_SKIP_VALIDATE := false
	GORELEASER_SKIP_PUBLISH  := release --skip-publish=false
else
	GORELEASER_SKIP_PUBLISH  := --skip-publish=true
	GORELEASER_SKIP_VALIDATE ?= false
	GITHUB_TOKEN=
endif

ifeq ($(GORELEASER_MOUNT_CONFIG),true)
	GORELEASER_IMAGE := -v $(HOME)/.docker/config.json:/root/.docker/config.json $(GORELEASER_IMAGE)
endif

DETECTED_OS := $(shell sh -c 'uname 2>/dev/null || echo Unknown')

# on MacOS disable deprecation warnings security framework
ifeq ($(DETECTED_OS), Darwin)
	export CGO_CFLAGS=-Wno-deprecated-declarations
endif

# if go.mod contains replace for any modules on local filesystem
# mount them into docker during goreleaser build to exactly same path
REPLACED_MODULES              := $(shell go list -mod=readonly -m -f '{{ .Replace }}' all 2>/dev/null | grep -v -x -F "<nil>" | grep "^/")
ifneq ($(REPLACED_MODULES), )
	GORELEASER_MOUNT_REPLACED := $(foreach mod, $(REPLACED_MODULES), -v $(mod):$(mod)\\)
endif
GORELEASER_MOUNT_REPLACED     := $(GORELEASER_MOUNT_REPLACED:\\=)

.PHONY: bins
bins: $(BINS)

.PHONY: build
build:
	$(GO_BUILD) -a  ./...

$(PROVIDER_SERVICES): modvendor
	$(GO_BUILD) -o $@ $(BUILD_FLAGS) ./cmd/provider-services

.PHONY: provider-services
provider-services: $(PROVIDER_SERVICES)

.PHONY: docgen
docgen: $(PROVIRER_DEVCACHE)
	@echo TODO

.PHONY: install
install:
	$(GO) install $(BUILD_FLAGS) ./cmd/provider-services

.PHONY: docker-image
docker-image: modvendor
	docker run \
		--rm \
		-e STABLE=$(IS_STABLE) \
		-e MOD="$(GO_MOD)" \
		-e BUILD_TAGS="$(BUILD_TAGS)" \
		-e BUILD_VARS="$(GORELEASER_BUILD_VARS)" \
		-e STRIP_FLAGS="$(GORELEASER_STRIP_FLAGS)" \
		-e LINKMODE="$(GO_LINKMODE)" \
		-e DOCKER_IMAGE=$(RELEASE_DOCKER_IMAGE) \
		-v /var/run/docker.sock:/var/run/docker.sock \
		$(GORELEASER_MOUNT_REPLACED) \
		-v $(shell pwd):/go/src/$(GO_MOD_NAME) \
		-w /go/src/$(GO_MOD_NAME) \
		$(GORELEASER_IMAGE) \
		-f .goreleaser-docker.yaml \
		--debug=$(GORELEASER_DEBUG) \
		--clean \
		--skip-validate \
		--skip-publish \
		--snapshot

.PHONY: gen-changelog
gen-changelog: $(GIT_CHGLOG)
	@echo "generating changelog to .cache/changelog"
	./script/genchangelog.sh "$(RELEASE_TAG)" .cache/changelog.md

.PHONY: release
release: modvendor gen-changelog
	docker run \
		--rm \
		-e STABLE=$(IS_STABLE) \
		-e MOD="$(GO_MOD)" \
		-e BUILD_TAGS="$(BUILD_TAGS)" \
		-e BUILD_VARS="$(GORELEASER_BUILD_VARS)" \
		-e STRIP_FLAGS="$(GORELEASER_STRIP_FLAGS)" \
		-e LINKMODE="$(GO_LINKMODE)" \
		-e GITHUB_TOKEN="$(GITHUB_TOKEN)" \
		-e GORELEASER_CURRENT_TAG="$(RELEASE_TAG)" \
		-e DOCKER_IMAGE=$(RELEASE_DOCKER_IMAGE) \
		-v /var/run/docker.sock:/var/run/docker.sock \
		$(GORELEASER_MOUNT_REPLACED) \
		-v $(shell pwd):/go/src/$(GO_MOD_NAME) \
		-w /go/src/$(GO_MOD_NAME)\
		$(GORELEASER_IMAGE) \
		-f "$(GORELEASER_CONFIG)" \
		$(GORELEASER_SKIP_PUBLISH) \
		--skip-validate=$(GORELEASER_SKIP_VALIDATE) \
		--debug=$(GORELEASER_DEBUG) \
		--clean \
		--release-notes=/go/src/$(GO_MOD_NAME)/.cache/changelog.md
