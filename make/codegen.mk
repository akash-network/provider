.PHONY: generate
generate: $(MOCKERY)
	$(GO) generate ./...

.PHONY: kubetypes
kubetypes: $(K8S_KUBE_CODEGEN)
	rm -rf pkg/client/*
	GOBIN=$(AP_DEVCACHE_BIN) $(K8S_KUBE_CODEGEN) all \
	github.com/akash-network/provider/pkg/client github.com/akash-network/provider/pkg/apis \
	"akash.network:v2beta1,v2beta2" \
	--go-header-file "pkg/apis/boilerplate.go.txt"

.PHONY: codegen
codegen: generate kubetypes
