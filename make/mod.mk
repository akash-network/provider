# Golang modules and vendoring

.PHONY: deps-tidy
deps-tidy:
	$(GO) mod tidy

.PHONY: deps-vendor
deps-vendor:
	go mod vendor

.PHONY: modsensure
modsensure: deps-tidy deps-vendor

.PHONY: modvendor
modvendor: $(MODVENDOR) modsensure
ifeq ($(GO_MOD), vendor)
modvendor:
	@echo "vendoring non-go files..."
	chmod +x $(K8S_GENERATE_GROUPS)
	$(MODVENDOR) -copy="**/Makefile **/*.sh **/*.mk" -include=github.com/akash-network/node
endif
