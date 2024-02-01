.PHONY: lint
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT_RUN) ./... --issues-exit-code=0 --deadline=20m

.PHONY: lint-%
lint-%: $(GOLANGCI_LINT)
	$(LINT) $*
