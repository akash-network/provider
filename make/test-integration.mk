BUILD_TAGS_K8S_INTEGRATION := k8s_integration
BUILD_TAGS_E2E := e2e integration

BUILD_TAGS_ALL := "$(BUILD_TAGS_K8S_INTEGRATION) $(BUILD_TAGS_E2E)"
TEST_MODULES ?= $(shell $(GO) list ./... | grep -v '/mocks\|/kubernetes_mock\|/pkg/client')

KIND_NAME := kube

include _run/common-kind-vars.mk

KIND_VARS              ?= KUBE_INGRESS_IP="$(KIND_K8S_IP)" KUBE_INGRESS_PORT="$(KIND_HTTP_PORT)"

# This is statically specified in the vagrant configuration
# todo @troian check it still necessary
KUBE_NODE_IP ?= 172.18.8.101
###############################################################################
###                           Integration                                   ###
###############################################################################

INTEGRATION_VARS := TEST_INTEGRATION=true

# test-e2e-integration runs the e2e suites in TestIntegrationTestSuite and the
# e2e package's cluster-free tests. CI splits the suites into shards that run on
# separate machines; E2E_SHARD_INDEX (1-based) and E2E_SHARD_TOTAL select one,
# and with neither set every suite runs:
#   E2E_SHARD_INDEX=2 E2E_SHARD_TOTAL=3 make test-e2e-integration
# Each suite is a subtest named after its type; to run one, use the go test
# command below with e.g. -run 'TestIntegrationTestSuite/E2EMigrateHostname$'.
.PHONY: test-e2e-integration
test-e2e-integration:
	# Assumes cluster created and configured:
	# ```
	# KUSTOMIZE_INSTALLS=akash-operator-inventory make kube-cluster-setup-e2e
	# ```
	$(KIND_VARS) $(INTEGRATION_VARS) $(GO_TEST) -count=1 -p 4 -tags "e2e" -v ./integration/... -run '^(TestIntegrationTestSuite|TestQueryAppWithRetriesResendsBody|TestRunE2ESuites)$$' -timeout 3000s

.PHONY: test-e2e-integration-k8s
test-e2e-integration-k8s:
	$(INTEGRATION_VARS) \
	KUBE_NODE_IP="$(KUBE_NODE_IP)" \
	KUBE_INGRESS_IP=127.0.0.1 \
	KUBE_INGRESS_PORT=10080 \
	$(GO_TEST) -count=1 -p 4 -tags "e2e $(BUILD_MAINNET)" -v ./integration/... -run TestIntegrationTestSuite

.PHONY: test-query-app
test-query-app:
	 $(INTEGRATION_VARS) $(KIND_VARS) $(GO_TEST) -p 4 -tags "$(BUILD_TAGS_E2E)" -v ./integration/... -run TestQueryApp

.PHONY: test-k8s-integration
test-k8s-integration:
	# Assumes cluster created and configured:
	# ```
	# KUSTOMIZE_INSTALLS=akash-operator-inventory make kube-cluster-setup-e2e
	# ```
	$(GO_TEST) -count=1 -v -tags "$(BUILD_TAGS_K8S_INTEGRATION)" ./pkg/apis/akash.network/v2beta2
	$(GO_TEST) -count=1 -v -tags "$(BUILD_TAGS_K8S_INTEGRATION)" ./cluster/kube


###############################################################################
###                           Misc tests                                    ###
###############################################################################

.PHONY: shellcheck
shellcheck:
	docker run --rm \
	--volume ${PWD}:/shellcheck \
	--entrypoint sh \
	koalaman/shellcheck-alpine:stable \
	-x /shellcheck/script/shellcheck.sh

.PHONY: test
test: $(AP_DEVCACHE) wasmvm-libs
	$(GO_TEST) -v $(BUILD_FLAGS) -timeout 300s $(TEST_MODULES)

.PHONY: test-nocache
test-nocache: $(AP_DEVCACHE) wasmvm-libs
	$(GO_TEST) $(BUILD_FLAGS) -count=1 $(TEST_MODULES)

.PHONY: test-full
test-full: $(AP_DEVCACHE) wasmvm-libs
	$(GO_TEST) -v $(BUILD_FLAGS) $(TEST_MODULES)

.PHONY: test-coverage
test-coverage: $(AP_DEVCACHE) wasmvm-libs
	./script/codecov.sh "$(AP_DEVCACHE_TESTS)" $(BUILD_TAGS_ALL)
