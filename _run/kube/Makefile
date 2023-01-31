KUBE_SETUP_PREREQUISITES   ?= \

KUBE_CLUSTER_CREATE_TARGET := kind

KUSTOMIZE_INSTALLS ?= \
	akash-operator-hostname \
	akash-operator-inventory \
	akash-operator-ip

export AKASH_NODE=http://localhost:26657
export AP_NODE=http://localhost:26657

include ../common.mk
include ../common-commands.mk
include ../common-kube.mk

SDL_PATH ?= grafana.yaml

GATEWAY_HOSTNAME ?= localhost
GATEWAY_HOST     ?= $(GATEWAY_HOSTNAME):8443
GATEWAY_ENDPOINT ?= https://$(GATEWAY_HOST)

.PHONY: provider-run
provider-run:
	$(PROVIDER_SERVICES) run \
		--from "$(PROVIDER_KEY_NAME)" \
		--cluster-k8s \
		--gateway-listen-address "$(GATEWAY_HOST)" \
		--deployment-ingress-static-hosts true \
		--deployment-ingress-domain "$(GATEWAY_HOSTNAME)" \
		--cluster-node-port-quantity 100 \
		--cluster-public-hostname "$(GATEWAY_HOSTNAME)" \
		--bid-price-strategy "randomRange" \
		--deployment-runtime-class "none" \
		--ip-operator=true

.PHONY: provider-lease-ping
provider-lease-ping:
	curl -sIH "Host: hello.localhost" localhost:$(KIND_HTTP_PORT)

.PHONY: hostname-operator
hostname-operator:
	$(PROVIDER_SERVICES) hostname-operator

.PHONY: clean-kube
clean-kube:

.PHONY: kube-deployments-rollout
kube-deployments-rollout:

.PHONY: kube-setup-kube
kube-setup-kube:
