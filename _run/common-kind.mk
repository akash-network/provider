# KIND_NAME NOTE: 'kind' string literal is default for the GH actions 
# KinD, it's fine to use other names locally, however in GH container name 
# is configured by engineerd/setup-kind. `kind-control-plane` is the docker
# image's name in GH Actions.
export KIND_NAME ?= $(shell basename $$PWD)

KIND_CREATE       := $(AP_RUN_DIR)/.kind-create

KINDEST_VERSION  ?= v1.22.2
KIND_IMG         ?= kindest/node:$(KINDEST_VERSION)

K8S_CONTEXT      ?= $(shell kubectl config current-context)

KIND_HTTP_PORT = $(shell docker inspect \
    --type container "$(KIND_NAME)-control-plane" \
    --format '{{index .NetworkSettings.Ports "80/tcp" 0 "HostPort"}}')

KIND_HTTP_IP = $(shell docker inspect \
    --type container "$(KIND_NAME)-control-plane" \
    --format '{{index .NetworkSettings.Ports "80/tcp" 0 "HostIp"}}')

KIND_K8S_IP = $(shell docker inspect \
    --type container "$(KIND_NAME)-control-plane" \
    --format '{{index .NetworkSettings.Ports "6443/tcp" 0 "HostIp"}}')

# KIND_PORT_BINDINGS deliberately redirects stderr to /dev/null
# in order to suppress message Error: No such object: kube-control-plane
# during cluster setup
# it is a bit doggy way but seems to do the job
KIND_PORT_BINDINGS = $(shell docker inspect "$(KIND_NAME)-control-plane" \
    --format '{{index .NetworkSettings.Ports "80/tcp" 0 "HostPort"}}' 2> /dev/null)

SETUP_KIND                := "$(AP_ROOT)/script/setup-kind.sh"
KIND_CONFIG               ?= default
KIND_CONFIG_FILE          := $(shell "$(SETUP_KIND)" config-file $$PWD $(KIND_CONFIG))

include ../common-kustomize.mk

# certain targets need to use bash
# detect where bash is installed
# use akash-node-ready target as example
BASH_PATH                 := $(shell which bash)

INGRESS_CONFIG_PATH       ?= ../ingress-nginx.yaml
CALICO_MANIFEST           ?= https://docs.projectcalico.org/v3.8/manifests/calico.yaml

AKASH_DOCKER_IMAGE        ?= ghcr.io/ovrclk/akash:latest-$(UNAME_ARCH)
DOCKER_IMAGE              ?= ghcr.io/ovrclk/provider-services:latest-$(UNAME_ARCH)

PROVIDER_HOSTNAME         ?= localhost
PROVIDER_HOST              = $(PROVIDER_HOSTNAME):$(KIND_HTTP_PORT)
PROVIDER_ENDPOINT          = http://$(PROVIDER_HOST)

METALLB_CONFIG_PATH       ?= ../metallb.yaml
METALLB_IP_CONFIG_PATH    ?= ../kind-config-metal-lb-ip.yaml
METALLB_SERVICE_PATH      ?= ../../_docs/provider/kube/metallb-service.yaml

KIND_ROLLOUT_TIMEOUT      ?= 90

.PHONY: app-http-port
app-http-port:
	@echo $(KIND_HTTP_PORT)

.PHONY: kind-k8s-ip
kind-k8s-ip:
	@echo $(KIND_K8S_IP)

.PHONY: kind-prepare-images
kind-prepare-images:
ifneq ($(SKIP_BUILD), true)
	make -C $(AP_ROOT) docker-image
ifeq ($(AKASH_SRC_IS_LOCAL), true)
	make -C $(AKASH_LOCAL_PATH) docker-image
else
	docker pull $(AKASH_DOCKER_IMAGE)
endif
endif

.PHONY: kind-upload-images
kind-upload-images: $(KIND)
ifeq ($(KIND_NAME), single)
	$(KIND) --name "$(KIND_NAME)" load docker-image "$(AKASH_DOCKER_IMAGE)"
endif
	$(KIND) --name "$(KIND_NAME)" load docker-image "$(DOCKER_IMAGE)"

.PHONY: kind-port-bindings
kind-port-bindings: $(KIND)
	@echo $(KIND_PORT_BINDINGS)

.PHONY: kind-cluster-setup
kind-cluster-setup: init \
	kind-prepare-images \
	kind-cluster-create \
	kind-setup-ingress \
	kind-upload-images \
	kustomize-init \
	kustomize-deploy-services \
	kind-deployments-rollout \
	kind-setup-$(KIND_NAME)

.PHONY: kind-cluster-setup-e2e
kind-cluster-setup-e2e: kind-cluster-create kind-cluster-setup-e2e-ci

.PHONY: kind-cluster-setup-e2e-ci
kind-cluster-setup-e2eci:
kind-cluster-setup-e2e-ci: \
	kind-setup-ingress \
	kind-upload-images \
	kustomize-init \
	kustomize-deploy-services \
	kind-deployments-rollout

$(KIND_CREATE): $(KIND) $(AP_RUN_DIR)
	$(KIND) create cluster --config "$(KIND_CONFIG_FILE)" --name "$(KIND_NAME)" --image "$(KIND_IMG)"
	touch $@

.INTERMEDIATE: kind-cluster-create
kind-cluster-create: $(KIND_CREATE)

.PHONY: kind-setup-ingress
kind-setup-ingress: kind-setup-ingress-$(KIND_CONFIG)

.PHONY: kind-setup-ingress-calico
kind-setup-ingress-calico:
	kubectl apply -f "$(CALICO_MANIFEST)"
	# Calico needs to be managing networking before finishing setup
	kubectl apply -f "$(INGRESS_CONFIG_PATH)"
	kubectl rollout status deployment -n ingress-nginx ingress-nginx-controller --timeout=$(KIND_ROLLOUT_TIMEOUT)s
	kubectl apply -f "$(METALLB_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_IP_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_SERVICE_PATH)"
	"$(SETUP_KIND)" calico-metrics

.PHONY: kind-setup-ingress-default
kind-setup-ingress-default:
	kubectl label nodes $(KIND_NAME)-control-plane akash.network/role=ingress
	kubectl apply -f "$(INGRESS_CONFIG_PATH)"
	kubectl rollout status deployment -n ingress-nginx ingress-nginx-controller --timeout=$(KIND_ROLLOUT_TIMEOUT)s
	kubectl apply -f "$(METALLB_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_IP_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_SERVICE_PATH)"
	"$(SETUP_KIND)"

.PHONY: kind-cluster-delete
kind-cluster-delete: $(KIND)
	$(KIND) delete cluster --name "$(KIND_NAME)"
	rm -rf $(KIND_CREATE)

.PHONY: kind-status-ingress-%
kind-status-ingress-%:
	kubectl rollout status -n akash-services ingress $* --timeout=$(KIND_ROLLOUT_TIMEOUT)s

.PHONY: kind-deployment-rollout-%
kind-deployment-rollout-%:
	kubectl -n akash-services rollout status deployment $* --timeout=$(KIND_ROLLOUT_TIMEOUT)s
	kubectl -n akash-services wait pods -l akash.network/component=$* --for condition=Ready --timeout=$(KIND_ROLLOUT_TIMEOUT)s

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
