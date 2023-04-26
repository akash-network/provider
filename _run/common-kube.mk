K8S_CONTEXT                ?= $(shell kubectl config current-context)
KUBE_CREATE                := $(AP_RUN_DIR)/.kube-create

include ../common-kustomize.mk
include ../common-kind.mk

KUBE_SSH_NODE_NAME         ?= akash-gpu
KUBE_UPLOAD_AKASH_IMAGE    ?= false
KUBE_CLUSTER_CREATE_TARGET ?= default
KUBE_ROLLOUT_TIMEOUT       ?= 180

INGRESS_CONFIG_PATH       ?= ../ingress-nginx.yaml
CALICO_MANIFEST           ?= https://github.com/projectcalico/calico/blob/v3.25.0/manifests/calico.yaml

# when image is built locally, for example on M1 (arm64) and kubernetes cluster is running on amd64
# we need to specify what arch to deploy as docker manifests can't be transferred locally
KUBE_DOCKER_IMAGE_ARCH    ?= amd64
#$(UNAME_ARCH)

ifeq ($(AKASH_SRC_IS_LOCAL), true)
AKASH_DOCKER_IMAGE        ?= ghcr.io/akash-network/node:latest-$(KUBE_DOCKER_IMAGE_ARCH)
else
AKASH_DOCKER_IMAGE        ?= ghcr.io/akash-network/node:$(AKASH_VERSION)-$(KUBE_DOCKER_IMAGE_ARCH)
endif
DOCKER_IMAGE              ?= ghcr.io/akash-network/provider:latest-$(KUBE_DOCKER_IMAGE_ARCH)

PROVIDER_HOSTNAME         ?= localhost
PROVIDER_HOST              = $(PROVIDER_HOSTNAME):$(KIND_HTTP_PORT)
PROVIDER_ENDPOINT          = http://$(PROVIDER_HOST)

METALLB_CONFIG_PATH       ?= ../metallb.yaml
METALLB_IP_CONFIG_PATH    ?= ../kube-config-metal-lb-ip.yaml
METALLB_SERVICE_PATH      ?= ../../_docs/provider/kube/metallb-service.yaml

DOCKER_LOAD_IMAGES        := $(DOCKER_IMAGE)
ifeq ($(KUBE_UPLOAD_AKASH_IMAGE), true)
DOCKER_LOAD_IMAGES += $(AKASH_DOCKER_IMAGE)
endif

.PHONY: kube-prepare-images
 kube-prepare-images:
ifneq ($(SKIP_BUILD), true)
	make -C $(AP_ROOT) docker-image
ifeq ($(AKASH_SRC_IS_LOCAL), true)
	make -C $(AKASH_LOCAL_PATH) docker-image
else
	docker pull $(AKASH_DOCKER_IMAGE)
endif
endif

.PHONY: kube-upload-images
kube-upload-images: kube-upload-images-$(KUBE_CLUSTER_CREATE_TARGET)

.PHONY: kube-upload-images-kind
kube-upload-images-kind: $(KIND)
	$(AP_ROOT)/script/load_docker2kind.sh "$(DOCKER_LOAD_IMAGES)" $(KIND_NAME)

.PHONY: kube-upload-images-default
kube-upload-images-default:
	$(AP_ROOT)/script/load_docker2ctr.sh "$(DOCKER_LOAD_IMAGES)" $(KUBE_SSH_NODE_NAME)

$(KUBE_CREATE): $(AP_RUN_DIR) kube-cluster-create-$(KUBE_CLUSTER_CREATE_TARGET)
	$(AP_ROOT)/script/setup-kube.sh $(KUBE_SSH_NODE_NAME) init
	#$(AP_ROOT)/script/setup-kube.sh crd
	touch $@

.INTERMEDIATE: kube-cluster-create-default
kube-cluster-create-default: $(KUBE_CREATE)

.PHONY: kube-cluster-check-alive
kube-cluster-check-info:
	kubectl cluster-info >/dev/null 2>&1 echo $?

.PHONY: kube-cluster-setup
kube-cluster-setup: init \
	kube-prepare-images \
	$(KUBE_CREATE) \
	kube-cluster-check-info \
	kube-setup-ingress \
	kube-upload-images \
	kustomize-init \
	kustomize-deploy-services \
	kube-deployments-rollout \
	kube-install-helm-charts \
	kube-setup-$(AP_RUN_NAME)

# dedicated target to setup cluster on local machine
.PHONY: kube-cluster-setup-e2e
kube-cluster-setup-e2e: $(KUBE_CREATE) kube-cluster-setup-e2e-ci

# dedicated target to perform setup within Github Actions CI
.PHONY: kube-cluster-setup-e2e-ci
kube-cluster-setup-e2e-ci: \
	kube-setup-ingress \
	kube-upload-images \
	kustomize-init \
	kustomize-deploy-services \
	kube-deployments-rollout \
	kube-install-helm-charts

.PHONY: kube-cluster-delete
kube-cluster-delete: kube-cluster-delete-$(KUBE_SSH_NODE_NAME)

.PHONY: kube-setup-ingress
kube-setup-ingress: kube-setup-ingress-$(KIND_CONFIG)

.PHONY: kube-setup-ingress-calico
kube-setup-ingress-calico:
	kubectl apply -f "$(CALICO_MANIFEST)"
	# Calico needs to be managing networking before finishing setup
	kubectl apply -f "$(INGRESS_CONFIG_PATH)"
	kubectl rollout status deployment -n ingress-nginx ingress-nginx-controller --timeout=$(KUBE_ROLLOUT_TIMEOUT)s
	kubectl apply -f "$(METALLB_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_IP_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_SERVICE_PATH)"
	"$(SETUP_KIND)" calico-metrics

.PHONY: kube-setup-ingress-default
kube-setup-ingress-default:
	kubectl label nodes $(KIND_NAME)-control-plane akash.network/role=ingress
	kubectl apply -f "$(INGRESS_CONFIG_PATH)"
	kubectl rollout status deployment -n ingress-nginx ingress-nginx-controller --timeout=$(KUBE_ROLLOUT_TIMEOUT)s
	kubectl apply -f "$(METALLB_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_IP_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_SERVICE_PATH)"
	"$(SETUP_KIND)"

.PHONY: kube-status-ingress-%
kube-status-ingress-%:
	kubectl rollout status -n akash-services ingress $* --timeout=$(KUBE_ROLLOUT_TIMEOUT)s

.PHONY: kube-deployment-rollout-%
kube-deployment-rollout-%:
	kubectl -n akash-services rollout status deployment $* --timeout=$(KUBE_ROLLOUT_TIMEOUT)s
	kubectl -n akash-services wait pods -l akash.network/component=$* --for condition=Ready --timeout=$(KUBE_ROLLOUT_TIMEOUT)s

.PHONY: akash-node-ready
akash-node-ready: SHELL=$(BASH_PATH)
akash-node-ready:
	@( \
		max_retry=15; \
		counter=0; \
		while [[ counter -lt max_retry ]]; do \
			read block < <(curl -s $(AKASH_NODE)/status | jq -r '.result.sync_info.latest_block_height' 2> /dev/null); \
			if [[ $$? -ne 0 || $$block -lt 1 ]]; then \
				echo "unable to get node status. sleep for 1s"; \
				((counter++)); \
				sleep 1; \
			else \
				echo "latest block height: $${block}"; \
				exit 0; \
			fi \
		done; \
		exit 1 \
	)
