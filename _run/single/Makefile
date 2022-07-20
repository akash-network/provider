KUSTOMIZE_INSTALLS ?= \
	akash-node \
	akash-provider \
	akash-operator-hostname \
	akash-operator-inventory

include ../common.mk
include ../common-commands.mk
include ../common-kind.mk

PROVIDER_HOSTNAME  = akash-provider.localhost
GATEWAY_ENDPOINT  ?= https://akash-provider.localhost

.PHONY: kind-namespace-setup
kind-namespace-setup:
	kubectl apply -f "$(KUSTOMIZE_ROOT)/networking"

.PHONY: provider-lease-ping
provider-lease-ping:
	curl -sIH "Host: hello.localhost" localhost:$(KIND_HTTP_PORT)

PHONY: clean-single
clean-single:

.PHONY: kind-deployments-rollout
kind-deployments-rollout: kind-deployment-rollout-akash-node

.PHONY: kind-setup-single
kind-setup-single: akash-node-ready provider-create
