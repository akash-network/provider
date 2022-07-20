KIND_SETUP_PREREQUISITES ?= \

KUSTOMIZE_INSTALLS ?= \
	akash-operator-hostname \
	akash-operator-inventory

include ../common.mk
include ../common-commands.mk
include ../common-kind.mk

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
		--deployment-runtime-class "none"

.PHONY: provider-lease-ping
provider-lease-ping:
	curl -sIH "Host: hello.localhost" localhost:$(KIND_HTTP_PORT)

.PHONY: hostname-operator
hostname-operator:
	$(PROVIDER_SERVICES) hostname-operator

.PHONY: clean-kube
clean-kube:

.PHONY: kind-deployments-rollout
kind-deployments-rollout:

.PHONY: kind-setup-kube
kind-setup-kube:
