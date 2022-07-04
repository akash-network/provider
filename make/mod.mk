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
	@echo "vendoring non-go files..."
	$(MODVENDOR) -copy="**/*.go.txt **/*.sh" -include=k8s.io/code-generator
