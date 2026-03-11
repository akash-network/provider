GORELEASER_RELEASE       ?= false
GORELEASER_DEBUG         ?= false
GORELEASER_IMAGE         := ghcr.io/goreleaser/goreleaser-cross:$(GOTOOLCHAIN_SEMVER)
GORELEASER_MOUNT_CONFIG  ?= false
GORELEASER_MOD_MOUNT     ?= $(shell cat $(ROOT_DIR)/.github/repo | tr -d '\n')

GORELEASER_SKIP_FLAGS    := $(GORELEASER_SKIP)
GORELEASER_SKIP          :=

null  :=
space := $(null) #
comma := ,

ifneq ($(GORELEASER_RELEASE),true)
	GITHUB_TOKEN=
	GORELEASER_SKIP_FLAGS += publish
endif

ifneq ($(GORELEASER_SKIP_FLAGS),)
	GORELEASER_SKIP := --skip=$(subst $(space),$(comma),$(strip $(GORELEASER_SKIP_FLAGS)))
endif

ifeq ($(GORELEASER_MOUNT_CONFIG),true)
	GORELEASER_IMAGE := -v $(HOME)/.docker/config.json:/root/.docker/config.json $(GORELEASER_IMAGE)
endif

DETECTED_OS := $(shell sh -c 'uname 2>/dev/null || echo Unknown')

# on MacOS disable deprecation warnings security framework
ifeq ($(DETECTED_OS), Darwin)
	export CGO_CFLAGS=-Wno-deprecated-declarations
endif

GORELEASER_GOWORK := off
ifneq ($(GOWORK), off)
	GORELEASER_GOWORK := /go/src/$(GORELEASER_MOD_MOUNT)/go.work
endif

.PHONY: bump-%
bump-%:
	@./script/tools.sh bump "$*"

.PHONY: bins
bins: $(BINS)

.PHONY: build
build: wasmvm-libs
	$(GO_BUILD) -a  ./...

.PHONY: $(PROVIDER_SERVICES)
$(PROVIDER_SERVICES): wasmvm-libs
	$(GO_BUILD) -o $@ $(BUILD_FLAGS) ./cmd/provider-services

.PHONY: provider-services
provider-services: $(PROVIDER_SERVICES)

.PHONY: docgen
docgen: $(AP_DEVCACHE)
	@echo TODO

.PHONY: install
install: wasmvm-libs
	$(GO) install $(BUILD_FLAGS) ./cmd/provider-services

.PHONY: chmod-akash-scripts
chmod-akash-scripts:
	find "$(AKASHD_LOCAL_PATH)/script" -type f -name '*.sh' -exec echo "chmod +x {}" \; -exec chmod +x {} \;

.PHONY: docker-image
docker-image: wasmvm-libs
	docker run \
		--rm \
		-e MOD="$(GO_MOD)" \
		-e BUILD_TAGS="$(GORELEASER_TAGS)" \
		-e BUILD_LDFLAGS="$(GORELEASER_LDFLAGS)" \
		-e DOCKER_IMAGE=$(RELEASE_DOCKER_IMAGE) \
		-e GOPATH=/go \
		-e GOTOOLCHAIN="$(GOTOOLCHAIN)" \
		-e GOWORK="$(GORELEASER_GOWORK)" \
		-v $(GOPATH):/go \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(shell pwd):/go/src/$(GO_MOD_NAME) \
		-w /go/src/$(GO_MOD_NAME) \
		$(GORELEASER_IMAGE) \
		-f .goreleaser-docker.yaml \
		--verbose=$(GORELEASER_DEBUG) \
		--clean \
		--skip=publish,validate \
		--snapshot

.PHONY: gen-changelog
gen-changelog: $(GIT_CHGLOG)
	@echo "generating changelog to .cache/changelog"
	./script/genchangelog.sh "$(RELEASE_TAG)" .cache/changelog.md

.PHONY: release
release: wasmvm-libs gen-changelog
	docker run \
		--rm \
		-e MOD="$(GO_MOD)" \
		-e BUILD_TAGS="$(GORELEASER_TAGS)" \
		-e BUILD_LDFLAGS="$(GORELEASER_LDFLAGS)" \
		-e GITHUB_TOKEN="$(GITHUB_TOKEN)" \
		-e GORELEASER_CURRENT_TAG="$(RELEASE_TAG)" \
		-e DOCKER_IMAGE=$(RELEASE_DOCKER_IMAGE) \
		-e GOTOOLCHAIN="$(GOTOOLCHAIN)" \
		-e GOPATH=/go \
		-e GOWORK="$(GORELEASER_GOWORK)" \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(GOPATH):/go \
		-v $(shell pwd):/go/src/$(GO_MOD_NAME) \
		-w /go/src/$(GO_MOD_NAME)\
		$(GORELEASER_IMAGE) \
		-f "$(GORELEASER_CONFIG)" \
		release \
		$(GORELEASER_SKIP) \
		--verbose=$(GORELEASER_DEBUG) \
		--clean \
		--release-notes=/go/src/$(GO_MOD_NAME)/.cache/changelog.md
