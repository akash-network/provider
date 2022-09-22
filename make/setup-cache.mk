ifeq (, $(AP_DEVCACHE))
$(error AP_DEVCACHE is not set)
endif

$(AP_DEVCACHE):
	@echo "creating .cache dir structure..."
	mkdir -p $@
	mkdir -p $(AP_DEVCACHE_BIN)
	mkdir -p $(AP_DEVCACHE_INCLUDE)
	mkdir -p $(AP_DEVCACHE_VERSIONS)
	mkdir -p $(AP_DEVCACHE_NODE_MODULES)
	mkdir -p $(AP_DEVCACHE_TESTS)
	mkdir -p $(AP_DEVCACHE)/run

.INTERMEDIATE: cache
cache: $(AP_DEVCACHE)

.PHONY: akash
ifeq ($(AKASH_SRC_IS_LOCAL), true)
akash:
	@echo "compiling and installing Akash from local sources"
	make -C $(AKASH_LOCAL_PATH) akash AKASH=$(AP_DEVCACHE_BIN)/akash
else
$(AKASH_VERSION_FILE): $(AP_DEVCACHE)
	@echo "Installing akash $(AKASH_VERSION) ..."
	rm -f $(AKASH)
	wget -q https://github.com/ovrclk/akash/releases/download/v$(AKASH_VERSION)/akash_$(AKASH_VERSION)_$(UNAME_OS_LOWER)_$(UNAME_ARCH).zip -O $(AP_DEVCACHE)/akash.zip
	unzip -p $(AP_DEVCACHE)/akash.zip akash_$(AKASH_VERSION)_$(UNAME_OS_LOWER)_$(UNAME_ARCH)/akash > $(AKASH)
	chmod +x $(AKASH)
	rm $(AP_DEVCACHE)/akash.zip
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
akash: $(AKASH_VERSION_FILE)
endif

$(STATIK_VERSION_FILE): $(AP_DEVCACHE)
	@echo "Installing statik $(STATIK_VERSION) ..."
	rm -f $(STATIK)
	GOBIN=$(AP_DEVCACHE_BIN) $(GO) install github.com/rakyll/statik@$(STATIK_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(STATIK): $(STATIK_VERSION_FILE)

$(MODVENDOR_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing modvendor $(MODVENDOR_VERSION) ..."
	rm -f $(MODVENDOR)
	GOBIN=$(AP_DEVCACHE_BIN) $(GO) install github.com/goware/modvendor@$(MODVENDOR_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(MODVENDOR): $(MODVENDOR_VERSION_FILE)

$(GIT_CHGLOG_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing git-chglog $(GIT_CHGLOG_VERSION) ..."
	rm -f $(GIT_CHGLOG)
	GOBIN=$(AP_DEVCACHE_BIN) go install github.com/git-chglog/git-chglog/cmd/git-chglog@$(GIT_CHGLOG_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(GIT_CHGLOG): $(GIT_CHGLOG_VERSION_FILE)

$(MOCKERY_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing mockery $(MOCKERY_VERSION) ..."
	rm -f $(MOCKERY)
	GOBIN=$(AP_DEVCACHE_BIN) go install -ldflags '-s -w -X github.com/vektra/mockery/v2/pkg/config.SemVer=$(MOCKERY_VERSION)' github.com/vektra/mockery/v2@v$(MOCKERY_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(MOCKERY): $(MOCKERY_VERSION_FILE)

$(GOLANGCI_LINT_VERSION_FILE): $(AP_DEVCACHE)
	@echo "installing golangci-lint $(GOLANGCI_LINT_VERSION) ..."
	rm -f $(MOCKERY)
	GOBIN=$(AP_DEVCACHE_BIN) go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(GOLANGCI_LINT): $(GOLANGCI_LINT_VERSION_FILE)

$(K8S_CODE_GEN_VERSION_FILE): $(AP_DEVCACHE) modvendor
	@echo "installing k8s code-generator $(K8S_CODE_GEN_VERSION) ..."
	rm -f $(K8S_GO_TO_PROTOBUF)
	GOBIN=$(AP_DEVCACHE_BIN) go install $(ROOT_DIR)/vendor/k8s.io/code-generator/...
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
	chmod +x $(K8S_GENERATE_GROUPS)
$(K8S_GO_TO_PROTOBUF): $(K8S_CODE_GEN_VERSION_FILE)
$(K8S_GENERATE_GROUPS): $(K8S_CODE_GEN_VERSION_FILE)

$(KIND_VERSION_FILE): $(AP_DEVCACHE)
ifeq (, $(KIND))
	@echo "installing kind $(KIND_VERSION) ..."
	rm -f $(MOCKERY)
	GOBIN=$(AP_DEVCACHE_BIN) go install sigs.k8s.io/kind
endif
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(KIND): $(KIND_VERSION_FILE)

$(NPM):
ifeq (, $(shell which $(NPM) 2>/dev/null))
	$(error "npm installation required")
endif

$(SWAGGER_COMBINE): $(AP_DEVCACHE) $(NPM)
ifeq (, $(shell which swagger-combine 2>/dev/null))
	@echo "Installing swagger-combine..."
	npm install swagger-combine --prefix $(AP_DEVCACHE_NODE_MODULES)
	chmod +x $(SWAGGER_COMBINE)
else
	@echo "swagger-combine already installed; skipping..."
endif

cache-clean:
	rm -rf $(AP_DEVCACHE)
