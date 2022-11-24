GORELEASER_RELEASE            ?= false
GORELEASER_DEBUG              ?= false
GORELEASER_IMAGE              := ghcr.io/goreleaser/goreleaser-cross:v$(GOLANG_VERSION)
GORELEASER_BIND_DOCKER_CONFIG ?= false

ifeq ($(GORELEASER_RELEASE),true)
	GORELEASER_SKIP_VALIDATE := false
	GORELEASER_SKIP_PUBLISH  := release --skip-publish=false
else
	GORELEASER_SKIP_PUBLISH  := --skip-publish=true
	GORELEASER_SKIP_VALIDATE ?= false
	GITHUB_TOKEN=
endif

GORELEASER_DOCKER_CONFIG :=

ifeq ($(GORELEASER_BIND_DOCKER_CONFIG),true)
	GORELEASER_DOCKER_CONFIG := -v $(HOME)/.docker/config.json:/root/.docker/config.json
endif

ifeq ($(OS),Windows_NT)
$(error Windows, really?)
else
	DETECTED_OS := $(shell sh -c 'uname 2>/dev/null || echo Unknown')
endif

# on MacOS disable deprecation warnings security framework
ifeq ($(DETECTED_OS), Darwin)
	export CGO_CFLAGS=-Wno-deprecated-declarations
endif

.PHONY: bins
bins: $(BINS)

.PHONY: build
build:
	$(GO) build -a  ./...

$(PROVIDER_SERVICES): modvendor
	$(GO) build -o $@ $(BUILD_FLAGS) ./cmd/provider-services

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
		-v /var/run/docker.sock:/var/run/docker.sock $(AKASH_BIND_LOCAL) \
		-v $(shell pwd):/go/src/$(GO_MOD_NAME) \
		-w /go/src/$(GO_MOD_NAME) \
		$(GORELEASER_IMAGE) \
		-f .goreleaser-docker-$(UNAME_ARCH).yaml \
		--debug=$(GORELEASER_DEBUG) \
		--rm-dist \
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
		-e HOMEBREW_NAME="$(GORELEASER_HOMEBREW_NAME)" \
		-e HOMEBREW_CUSTOM="$(GORELEASER_HOMEBREW_CUSTOM)" \
		-e GITHUB_TOKEN="$(GITHUB_TOKEN)" \
		-e GORELEASER_CURRENT_TAG="$(RELEASE_TAG)" \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(shell pwd):/go/src/$(GO_MOD_NAME) \
		$(GORELEASER_DOCKER_CONFIG) \
		-w /go/src/$(GO_MOD_NAME)\
		$(GORELEASER_IMAGE) \
		-f "$(GORELEASER_CONFIG)" \
		$(GORELEASER_SKIP_PUBLISH) \
		--skip-validate=$(GORELEASER_SKIP_VALIDATE) \
		--debug=$(GORELEASER_DEBUG) \
		--rm-dist \
		--release-notes=/go/src/$(GO_MOD_NAME)/.cache/changelog.md
