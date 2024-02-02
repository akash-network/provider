KUBE_SETUP_PREREQUISITES ?= \

KUBE_UPLOAD_AKASH_IMAGE    ?= true
KUBE_CLUSTER_CREATE_TYPE := ssh

KUBE_DOCKER_IMAGE_ARCH := amd64

KUSTOMIZE_INSTALLS ?= \
	akash-operator-hostname \
	akash-operator-inventory \
	akash-operator-ip

SDL_PATH ?= gpu.yaml

include ../common.mk
include ../common-commands.mk
include ../common-kube.mk

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
		--ip-operator=false

.PHONY: provider-lease-ping
provider-lease-ping:
	curl -sIH "Host: hello.localhost" localhost:$(KIND_HTTP_PORT)

.PHONY: clean-gpu
clean-gpu:

.PHONY: kind-deployments-rollout
kind-deployments-rollout:

.PHONY: kube-cluster-setup-gpu
kube-cluster-setup-gpu: init \
	kube-prepare-images \
	kube-upload-images \
	kustomize-init \
	kustomize-deploy-services \
	kube-deployments-rollout \
	kind-setup-$(KIND_NAME)
