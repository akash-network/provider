ifeq (, $(AP_DEVCACHE))
$(error AP_DEVCACHE is not set)
endif

ifeq (, $(AKASH))
$(error "AKASH variable is not set")
endif

ifeq (, $(AKT))
$(error "AKT variable is not set")
endif

$(AP_DEVCACHE):
	@echo "creating .cache dir structure..."
	mkdir -p $@
	mkdir -p $(AP_DEVCACHE_BIN)
	mkdir -p $(AP_DEVCACHE_LIB)
	mkdir -p $(AP_DEVCACHE_INCLUDE)
	mkdir -p $(AP_DEVCACHE_VERSIONS)
	mkdir -p $(AP_DEVCACHE_NODE_MODULES)
	mkdir -p $(AP_DEVCACHE_TESTS)
	mkdir -p $(AP_DEVCACHE)/run

.INTERMEDIATE: cache
cache: $(AP_DEVCACHE)

AKASH_INSTALL_ARCH := $(UNAME_ARCH)
# darwin has option to install multi-arch binary
ifeq ($(UNAME_OS_LOWER), darwin)
	AKASH_INSTALL_ARCH := all
endif

.PHONY: rm-akash
rm-akash:
	rm -f $(AKASHD_VERSION_FILE)

.PHONY: akash
ifeq ($(AKASHD_BUILD_FROM_SRC), true)
akash: rm-akash
	@echo "compiling and installing Akash from local sources"
	$(AP_ROOT)/script/tools.sh build-akash $(AKASHD_LOCAL_PATH)
#make -C $(AKASHD_LOCAL_PATH) akash AKASH=$(AP_DEVCACHE_BIN)/akash
else
$(AKASHD_VERSION_FILE): | $(AP_DEVCACHE)
	@echo "Installing akash $(AKASHD_VERSION) ..."
	rm -f $(AKASH)
	wget -q https://github.com/akash-network/node/releases/download/v$(AKASHD_VERSION)/akash_$(UNAME_OS_LOWER)_$(AKASH_INSTALL_ARCH).zip -O $(AP_DEVCACHE)/akash.zip
	unzip -p $(AP_DEVCACHE)/akash.zip akash > $(AKASH)
	chmod +x $(AKASH)
	rm $(AP_DEVCACHE)/akash.zip
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
akash: $(AKASHD_VERSION_FILE)
endif

.PHONY: rm-akt
rm-akt:
	rm -f $(AKT_VERSION_FILE)

# akt for the host in $(AKT), plus the linux binaries copied into provider
# images in $(AKT_DOCKER_DIR)/linux_<arch>/akt
.PHONY: akt
ifeq ($(AKT_BUILD_FROM_SRC), true)
ifeq (,$(AKT_LOCAL_SRC))
# the release check (make/init.mk) found no release asset and there is no
# checkout to build from; fail before rm-akt drops the installed version
akt:
	@echo "akt $(AKT_VERSION): release asset akt_$(AKT_VERSION:v%=%)_darwin_all.zip not found; set AKT_LOCAL_PATH to an akt checkout to build it from source" >&2
	@exit 1
else
akt: rm-akt
	@echo "compiling and installing AKT CLI from local sources"
	$(AP_ROOT)/script/tools.sh build-akt "$(AKT_LOCAL_SRC)" "$(AKT)"
	$(AP_ROOT)/script/tools.sh build-akt-docker "$(AKT_LOCAL_SRC)" "$(AKT_DOCKER_DIR)"
endif
else
$(AKT_VERSION_FILE): | $(AP_DEVCACHE)
	@echo "Installing AKT $(AKT_VERSION) ..."
	rm -f $(AKT) $(AKT_DOCKER_DIR)/linux_amd64/akt $(AKT_DOCKER_DIR)/linux_arm64/akt
	( \
		tmpfile=$$(mktemp) || exit; \
		trap 'rm -f "$$tmpfile"' EXIT; \
		curl -fsSL -o "$$tmpfile" "https://github.com/akash-network/akt/releases/download/$(AKT_VERSION)/install.sh" && \
		gh attestation verify --repo=akash-network/akt \
		  --signer-workflow=akash-network/akt/.github/workflows/release.yml "$$tmpfile" && \
		sh "$$tmpfile" -b "$(AP_DEVCACHE_BIN)" && \
		BINSTALLER_OS=linux BINSTALLER_ARCH=amd64 sh "$$tmpfile" -b "$(AKT_DOCKER_DIR)/linux_amd64" && \
		BINSTALLER_OS=linux BINSTALLER_ARCH=arm64 sh "$$tmpfile" -b "$(AKT_DOCKER_DIR)/linux_arm64" \
	)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
akt: $(AKT_VERSION_FILE)
endif

$(STATIK_VERSION_FILE): | $(AP_DEVCACHE)
	@echo "Installing statik $(STATIK_VERSION) ..."
	rm -f $(STATIK)
	GOBIN=$(AP_DEVCACHE_BIN) $(GO) install github.com/rakyll/statik@$(STATIK_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(STATIK): $(STATIK_VERSION_FILE)

$(GIT_CHGLOG_VERSION_FILE): | $(AP_DEVCACHE)
	@echo "installing git-chglog $(GIT_CHGLOG_VERSION) ..."
	rm -f $(GIT_CHGLOG)
	GOBIN=$(AP_DEVCACHE_BIN) go install github.com/git-chglog/git-chglog/cmd/git-chglog@$(GIT_CHGLOG_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(GIT_CHGLOG): $(GIT_CHGLOG_VERSION_FILE)

$(MOCKERY_VERSION_FILE): | $(AP_DEVCACHE)
	@echo "installing mockery $(MOCKERY_VERSION) ..."
	rm -f $(MOCKERY)
	GOBIN=$(AP_DEVCACHE_BIN) go install -ldflags '-s -w -X $(MOCKERY_PACKAGE_NAME)/pkg/config.SemVer=$(MOCKERY_VERSION)' $(MOCKERY_PACKAGE_NAME)@$(MOCKERY_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(MOCKERY): $(MOCKERY_VERSION_FILE)

$(GOLANGCI_LINT_VERSION_FILE): $(SEMVER) | $(AP_DEVCACHE)
	@echo "installing golangci-lint $(GOLANGCI_LINT_VERSION) ..."
	rm -f $(GOLANGCI_LINT)
	GOBIN=$(AP_DEVCACHE_BIN) \
		go install github.com/golangci/golangci-lint/v$(shell $(SEMVER) get major $(GOLANGCI_LINT_VERSION))/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(GOLANGCI_LINT): $(GOLANGCI_LINT_VERSION_FILE)

$(K8S_CODEGEN_VERSION_FILE): | $(AP_DEVCACHE)
	@echo "installing k8s code-generator $(K8S_CODEGEN_VERSION) ..."
	rm -f $(K8S_GO_TO_PROTOBUF)
	GOBIN=$(AP_DEVCACHE_BIN) go install k8s.io/code-generator/...
	wget -q https://raw.githubusercontent.com/kubernetes/code-generator/$(K8S_CODEGEN_VERSION)/$(K8S_KUBE_CODEGEN_FILE) -O $(K8S_KUBE_CODEGEN)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
	chmod +x $(K8S_KUBE_CODEGEN)
$(K8S_KUBE_CODEGEN): $(K8S_CODEGEN_VERSION_FILE)
$(K8S_GO_TO_PROTOBUF): $(K8S_CODEGEN_VERSION_FILE)

$(SEMVER_VERSION_FILE): | $(AP_DEVCACHE)
	@echo "installing semver $(SEMVER_VERSION) ..."
	rm -f $(SEMVER)
	(cd $(GO_ROOT); GOBIN=$(AP_DEVCACHE_BIN) go install github.com/troian/semver/cmd/semver@$(SEMVER_VERSION))
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(SEMVER): $(SEMVER_VERSION_FILE)

ifeq (false, $(_SYSTEM_KIND))
$(KIND_VERSION_FILE): | $(AP_DEVCACHE)
	@echo "installing kind $(KIND_VERSION) ..."
	GOBIN=$(AP_DEVCACHE_BIN) go install sigs.k8s.io/kind@$(KIND_VERSION)
	rm -rf "$(dir $@)"
	mkdir -p "$(dir $@)"
	touch $@
$(KIND): $(KIND_VERSION_FILE)
endif

cache-clean:
	rm -rf $(AP_DEVCACHE)

$(AP_DEVCACHE_LIB)/%:
	wget -q --show-progress https://github.com/CosmWasm/wasmvm/releases/download/$(WASMVM_VERSION)/$* -O $@
	@rm -f $(AP_DEVCACHE_LIB)/.wasmvm_verified

$(AP_DEVCACHE_LIB)/wasmvm_checksums.txt:
	wget -q --show-progress https://github.com/CosmWasm/wasmvm/releases/download/$(WASMVM_VERSION)/checksums.txt -O $@
	@rm -f $(AP_DEVCACHE_LIB)/.wasmvm_verified

$(AP_DEVCACHE_LIB)/.wasmvm_verified: $(patsubst %, $(AP_DEVCACHE_LIB)/%,$(WASMVM_LIBS)) $(AP_DEVCACHE_LIB)/wasmvm_checksums.txt
	cd $(AP_DEVCACHE_LIB) && sha256sum -c --ignore-missing wasmvm_checksums.txt
	@touch $@

.PHONY: wasmvm-libs-verify
wasmvm-libs-verify:
	@$(MAKE) -s $(AP_DEVCACHE_LIB)/.wasmvm_verified

.NOTPARALLEL: wasmvm-libs
.PHONY: wasmvm-libs
wasmvm-libs: $(AP_DEVCACHE) $(patsubst %, $(AP_DEVCACHE_LIB)/%,$(WASMVM_LIBS)) $(AP_DEVCACHE_LIB)/wasmvm_checksums.txt wasmvm-libs-verify
