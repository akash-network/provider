GATEWAY_API          ?= false
CONFIDENTIAL_COMPUTE ?= false

KUBE_SETUP_PREREQUISITES   ?= \

KUBE_CLUSTER_CREATE_TYPE := kind
KUBE_SSH_NODES           := kind

# Auto-detect CC from cached cluster state (authoritative once set).
# Must be before includes so KUSTOMIZE_INSTALLS is complete when
# common-kustomize.mk expands its prerequisite lists.
CC_MARKER := $(AP_RUN_DIR)/.confidential-compute
ifneq ($(wildcard $(CC_MARKER)),)
CONFIDENTIAL_COMPUTE := true
endif

# Auto-detect Gateway API from cached cluster state (authoritative once set).
# Mirrors the CC marker above; must be before includes so downstream
# conditionals (KIND_CONFIG_FILE, provider-run --ingress-mode flag) observe it.
GW_MARKER := $(AP_RUN_DIR)/.gateway-api
ifneq ($(wildcard $(GW_MARKER)),)
GATEWAY_API := true
endif

KUSTOMIZE_INSTALLS ?= \
	akash-operator-hostname \
	akash-operator-inventory \
	akash-operator-ip

ifeq ($(CONFIDENTIAL_COMPUTE),true)
KUSTOMIZE_INSTALLS += confidential-compute
endif

# NOTE: Gateway API intentionally does NOT extend KUSTOMIZE_INSTALLS. Unlike the
# CC RuntimeClasses (deployed via the generic client-side kustomize-deploy-%
# pipeline in the services phase), nginx-gateway-fabric requires its CRDs applied
# server-side and installed during the ingress phase (before provider services and
# MetalLB). It is therefore wired through kube-setup-ingress via the
# kube-setup-ingress-default -> kube-setup-ingress-gateway override below.

include ../common.mk
include ../common-commands.mk
include ../common-kube.mk

# Gateway API mode overrides
ifeq ($(GATEWAY_API),true)
KIND_CONFIG_FILE := $(CURDIR)/kind-config-gateway.yaml

# Override default ingress setup to use Gateway API instead of NGINX Ingress
kube-setup-ingress-default: kube-setup-ingress-gateway
	@echo "Gateway API ingress setup complete"
endif

# CC mode: use Kind config with containerd runtime patches (no restart needed)
ifeq ($(CONFIDENTIAL_COMPUTE),true)
KIND_CONFIG_FILE := $(CURDIR)/kind-config-cc.yaml
endif

DEV_REGISTRY             ?= ghcr.io/akash-network
DEV_COMMIT               := $(shell git rev-parse --short HEAD)

ifeq ($(CONFIDENTIAL_COMPUTE),true)
SDL_PATH                   := deployment-cc.yaml
PROVIDER_CONFIG_PATH       := provider-cc.yaml
ATTESTATION_SIDECAR_IMAGE  ?= $(DEV_REGISTRY)/attestation-sidecar:$(DEV_COMMIT)

# Sidecar image flows through the standard image pipeline
DOCKER_LOAD_IMAGES += $(ATTESTATION_SIDECAR_IMAGE)

kube-prepare-images: kube-prepare-image-attestation-sidecar
else
SDL_PATH                   ?= grafana.yaml
endif

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
		--ip-operator=true \
		$(if $(filter true,$(GATEWAY_API)),--ingress-mode=gateway-api) \
		$(if $(filter true,$(CONFIDENTIAL_COMPUTE)),\
			--attestation-webhook-enabled=true \
			--attestation-sidecar-image="$(ATTESTATION_SIDECAR_IMAGE)" \
			--attestation-mock=true \
		)

.PHONY: provider-lease-ping
provider-lease-ping:
	curl -sIH "Host: hello.localhost" localhost:$(KIND_HTTP_PORT)

.PHONY: hostname-operator
hostname-operator:
	$(PROVIDER_SERVICES) hostname-operator

# CC kustomize pipeline hooks: confidential-compute has no template dir,
# no images to set, and no service configuration. The deploy step uses
# the standard kustomize-deploy-% pattern from common-kustomize.mk.
.PHONY: kustomize-template-confidential-compute
kustomize-template-confidential-compute:
	@echo "  [CC] Copying confidential-compute kustomize resources..."
	cp -r $(KUSTOMIZE_ROOT)/confidential-compute $(KUSTOMIZE_DIR)/

.PHONY: kustomize-set-image-confidential-compute
kustomize-set-image-confidential-compute:
	@echo "  [CC] No images to configure for RuntimeClasses"

.PHONY: kustomize-configure-confidential-compute
kustomize-configure-confidential-compute:
	@echo "  [CC] No service configuration needed for RuntimeClasses"

.PHONY: kube-prepare-image-attestation-sidecar
kube-prepare-image-attestation-sidecar:
ifneq ($(SKIP_BUILD), true)
	@echo "  [CC] Building attestation sidecar image..."
	$(MAKE) -C $(AP_ROOT)/sidecar/attestation docker DEV_REGISTRY=$(DEV_REGISTRY) DEV_COMMIT=$(DEV_COMMIT)
endif

.PHONY: clean-kube
clean-kube:
	rm -f $(CC_MARKER)
	rm -f $(GW_MARKER)

.PHONY: kube-deployments-rollout
kube-deployments-rollout: #$(patsubst %, kube-deployment-rollout-%,$(KUSTOMIZE_INSTALLS))

# Gateway API and confidential compute are orthogonal: append each mode's setup
# as an independent prerequisite so both run when both flags are true.
KUBE_SETUP_KUBE_DEPS :=
ifeq ($(GATEWAY_API),true)
KUBE_SETUP_KUBE_DEPS += kube-setup-gateway
endif
ifeq ($(CONFIDENTIAL_COMPUTE),true)
KUBE_SETUP_KUBE_DEPS += kube-setup-confidential-compute
endif

.PHONY: kube-setup-kube
kube-setup-kube: $(KUBE_SETUP_KUBE_DEPS)

.PHONY: kube-setup-gateway
kube-setup-gateway: kube-setup-hostname-operator-gateway
	@echo "  [GW] Persisting Gateway API configuration to cache..."
	@touch $(GW_MARKER)

.PHONY: kube-setup-confidential-compute
kube-setup-confidential-compute:
	@echo "  [CC] Persisting CC configuration to cache..."
	@touch $(CC_MARKER)
	@echo "  [CC] Labeling nodes for confidential compute scheduling..."
	@for node in $$(kubectl get nodes -o name); do \
		kubectl label $$node \
			katacontainers.io/kata-runtime=true \
			amd.feature.node.kubernetes.io/snp=true \
			intel.feature.node.kubernetes.io/tdx=true \
			nvidia.com/cc.ready.state=true \
			--overwrite; \
	done
	@echo "  [CC] Dev cluster setup complete (RuntimeClasses + node labels + sidecar)."

.PHONY: kube-setup-ingress-gateway
kube-setup-ingress-gateway:
	kubectl kustomize $(KUSTOMIZE_ROOT)/nginx-gateway-fabric/crds | kubectl apply --server-side -f -
	kubectl kustomize $(KUSTOMIZE_ROOT)/nginx-gateway-fabric | kubectl apply -f -
	kubectl apply -f "$(METALLB_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_IP_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_SERVICE_PATH)"
	@echo "Waiting for Gateway to be ready..."
	@kubectl wait --for=condition=Programmed gateway/akash-gateway -n akash-gateway --timeout=120s

.PHONY: kube-setup-hostname-operator-gateway
kube-setup-hostname-operator-gateway:
	@echo "Configuring hostname operator for Gateway API mode..."
	kubectl wait --for=condition=available deployment/operator-hostname -n akash-services --timeout=90s
	kubectl patch configmap operator-hostname -n akash-services --type=strategic --patch-file hostname-operator-gateway-patch.yaml
	kubectl rollout restart deployment/operator-hostname -n akash-services
	kubectl rollout status deployment/operator-hostname -n akash-services --timeout=60s

.PHONY: kube-cluster-delete
kube-cluster-delete: $(KIND)
	$(KIND) delete cluster --name "$(KIND_NAME)"
	rm -rf $(KUBE_CREATE)
	rm -f $(CC_MARKER)
	rm -f $(GW_MARKER)
