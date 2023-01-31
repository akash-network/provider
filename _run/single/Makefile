KUSTOMIZE_INSTALLS ?= \
	akash-node \
	akash-provider \
	akash-operator-hostname \
	akash-operator-inventory

KUBE_UPLOAD_AKASH_IMAGE := true

export AKASH_NODE               = http://akash.localhost:$(KIND_PORT_BINDINGS)
export AP_NODE                  = $(AKASH_NODE)

include ../common.mk
include ../common-commands.mk
include ../common-kube.mk

PROVIDER_HOSTNAME  = akash-provider.localhost
GATEWAY_ENDPOINT  ?= https://akash-provider.localhost

.PHONY: kube-namespace-setup
kube-namespace-setup:
	kubectl apply -f "$(KUSTOMIZE_ROOT)/networking"

.PHONY: provider-lease-ping
provider-lease-ping:
	curl -sIH "Host: hello.localhost" localhost:$(KIND_HTTP_PORT)

PHONY: clean-single
clean-single:

.PHONY: kube-deployments-rollout
kube-deployments-rollout: kind-deployment-rollout-akash-node

.PHONY: kube-setup-single
kube-setup-single: akash-node-ready provider-create
