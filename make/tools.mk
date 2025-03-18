.PHONY: tools
tools: $(GOLANGCI_LINT) $(STATIK) $(GIT_CHGLOG) $(MOCKERY)

# Install golangci-lint
$(GOLANGCI_LINT): $(GOLANGCI_LINT_VERSION_FILE)
	@echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."
	@GOBIN=$(AP_DEVCACHE_BIN) go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@touch $@

# Install statik
$(STATIK): $(STATIK_VERSION_FILE)
	@echo "Installing statik $(STATIK_VERSION)..."
	@GOBIN=$(AP_DEVCACHE_BIN) go install github.com/rakyll/statik@$(STATIK_VERSION)
	@touch $@

# Install git-chglog
$(GIT_CHGLOG): $(GIT_CHGLOG_VERSION_FILE)
	@echo "Installing git-chglog $(GIT_CHGLOG_VERSION)..."
	@GOBIN=$(AP_DEVCACHE_BIN) go install github.com/git-chglog/git-chglog/cmd/git-chglog@$(GIT_CHGLOG_VERSION)
	@touch $@

# Install mockery
$(MOCKERY): $(MOCKERY_VERSION_FILE)
	@echo "Installing mockery $(MOCKERY_VERSION)..."
	@GOBIN=$(AP_DEVCACHE_BIN) go install $(MOCKERY_PACKAGE_NAME)@$(MOCKERY_VERSION)
	@touch $@

# Install kind if not installed system-wide
.PHONY: kind
kind: $(KIND_VERSION_FILE)
	@if [ "$(_SYSTEM_KIND)" = "false" ]; then \
		echo "Installing kind $(KIND_VERSION)..."; \
		GOBIN=$(AP_DEVCACHE_BIN) go install sigs.k8s.io/kind@$(KIND_VERSION); \
		touch $@; \
	fi 