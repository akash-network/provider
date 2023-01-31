.PHONY: generate
generate:
	$(GO) generate ./...

.PHONY: mocks
mocks: $(MOCKERY) modvendor
	$(MOCKERY) --case=underscore --dir vendor/k8s.io/client-go/kubernetes --output testutil/kubernetes_mock --all --recursive --outpkg kubernetes_mocks --keeptree
	$(MOCKERY) --case=underscore --dir .                                       --output mocks              --name StatusClient
	$(MOCKERY) --case=underscore --dir .                                       --output mocks              --name Client
	$(MOCKERY) --case=underscore --dir cluster                                 --output cluster/mocks      --name Client
	$(MOCKERY) --case=underscore --dir cluster                                 --output cluster/mocks      --name ReadClient
	$(MOCKERY) --case=underscore --dir cluster                                 --output cluster/mocks      --name Cluster
	$(MOCKERY) --case=underscore --dir cluster                                 --output cluster/mocks      --name Service
	$(MOCKERY) --case=underscore --dir cluster/kube/metallb                    --output cluster/mocks      --name Client --structname MetalLBClient --filename metallb_client.go
	$(MOCKERY) --case=underscore --dir cluster/operatorclients                 --output cluster/mocks      --name IPOperatorClient
	$(MOCKERY) --case=underscore --dir cluster/types/v1beta3                   --output cluster/mocks      --name Deployment
	$(MOCKERY) --case=underscore --dir cluster/types/v1beta3                   --output cluster/mocks      --name HostnameServiceClient
	$(MOCKERY) --case=underscore --dir cluster/types/v1beta3                   --output cluster/mocks      --name Reservation
	$(MOCKERY) --case=underscore --dir manifest                                --output manifest/mocks     --name Client
	$(MOCKERY) --case=underscore --dir manifest                                --output manifest/mocks     --name StatusClient

.PHONY: kubetypes
kubetypes: $(K8S_GENERATE_GROUPS)
	GOBIN=$(AP_DEVCACHE_BIN) $(K8S_GENERATE_GROUPS) all \
	github.com/akash-network/provider/pkg/client github.com/akash-network/provider/pkg/apis \
	"akash.network:v2beta1,v2beta2" \
	--go-header-file "pkg/apis/boilerplate.go.txt"

.PHONY: codegen
codegen: generate kubetypes mocks
