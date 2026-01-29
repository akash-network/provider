.PHONY: generate
generate: $(MOCKERY)
	$(GO) generate ./...

.PHONY: kubetypes
kubetypes: $(K8S_KUBE_CODEGEN)
	./script/tools.sh k8s-gen

.PHONY: codegen
codegen: generate kubetypes

.PHONY: mocks
mocks: $(MOCKERY)
	$(MOCKERY)
