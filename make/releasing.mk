GORELEASER_RELEASE       ?= false
GORELEASER_DEBUG         ?= false
GORELEASER_IMAGE         := ghcr.io/goreleaser/goreleaser-cross:$(GOTOOLCHAIN_SEMVER)
GORELEASER_MOUNT_CONFIG  ?= false

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

.PHONY: bins
bins: $(BINS)

.PHONY: build
build:
	$(GO_BUILD) -a  ./...

.PHONY: $(PROVIDER_SERVICES)
$(PROVIDER_SERVICES):
	$(GO_BUILD) -o $@ $(BUILD_FLAGS) ./cmd/provider-services

.PHONY: provider-services
provider-services: $(PROVIDER_SERVICES)

.PHONY: docgen
docgen: $(AP_DEVCACHE)
	@echo TODO

.PHONY: install
install:
	$(GO) install $(BUILD_FLAGS) ./cmd/provider-services

.PHONY: chmod-akash-scripts
chmod-akash-scripts:
	find "$(AKASHD_LOCAL_PATH)/script" -type f -name '*.sh' -exec echo "chmod +x {}" \; -exec chmod +x {} \;

.PHONY: docker-image
docker-image:
	docker run \
		--rm \
		-e STABLE=$(IS_STABLE) \
		-e MOD="$(GO_MOD)" \
		-e BUILD_TAGS="$(BUILD_TAGS)" \
		-e BUILD_VARS="$(GORELEASER_BUILD_VARS)" \
		-e STRIP_FLAGS="$(GORELEASER_STRIP_FLAGS)" \
		-e LINKMODE="$(GO_LINKMODE)" \
		-e DOCKER_IMAGE=$(RELEASE_DOCKER_IMAGE) \
		-e GOPATH=/go \
		-e GOTOOLCHAIN="$(GOTOOLCHAIN)" \
		-v $(GOPATH):/go \
		-v /var/run/docker.sock:/var/run/docker.sock \
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
release: gen-changelog
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
		-e GOTOOLCHAIN="$(GOTOOLCHAIN)" \
		-e GOPATH=/go \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(GOPATH):/go \
		-v $(shell pwd):/go/src/$(GO_MOD_NAME) \
		-w /go/src/$(GO_MOD_NAME)\
		$(GORELEASER_IMAGE) \
		-f "$(GORELEASER_CONFIG)" \
		$(GORELEASER_SKIP_PUBLISH) \
		--skip-validate=$(GORELEASER_SKIP_VALIDATE) \
		--debug=$(GORELEASER_DEBUG) \
		--clean \
		--release-notes=/go/src/$(GO_MOD_NAME)/.cache/changelog.md
