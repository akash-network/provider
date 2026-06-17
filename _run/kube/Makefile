GATEWAY_API          ?= false
CONFIDENTIAL_COMPUTE ?= false

KUBE_SETUP_PREREQUISITES   ?= \

KUBE_CLUSTER_CREATE_TYPE := kind
KUBE_SSH_NODES           := kind

KUSTOMIZE_INSTALLS ?= \
	akash-operator-hostname \
	akash-operator-inventory \
	akash-operator-ip

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

# Confidential compute mode overrides
DEV_REGISTRY             ?= ghcr.io/cloud-j-luna
DEV_COMMIT               := $(shell git rev-parse --short HEAD)

ifeq ($(CONFIDENTIAL_COMPUTE),true)
SDL_PATH                   := deployment-cc.yaml
PROVIDER_CONFIG_PATH       := provider-cc.yaml
ATTESTATION_SIDECAR_IMAGE  ?= $(DEV_REGISTRY)/attestation-sidecar:$(DEV_COMMIT)
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

# Confidential compute cluster setup: creates RuntimeClass objects and labels
# the Kind node with TEE capability labels so the provider can schedule CC
# workloads. The pods won't actually run with Kata (Kind doesn't have it),
# but the webhook, annotations, and sidecar injection can be verified.
.PHONY: kube-setup-cc
kube-setup-cc:
	kubectl apply -f cc-setup.yaml
	@echo "Labeling kind node for CC..."
	@for node in $$(kubectl get nodes -o name); do \
		kubectl label $$node \
			katacontainers.io/kata-runtime=true \
			amd.feature.node.kubernetes.io/snp=true \
			intel.feature.node.kubernetes.io/tdx=true \
			nvidia.com/cc.ready.state=true \
			--overwrite; \
	done
	@echo "Building and loading attestation sidecar image into Kind..."
	$(MAKE) -C $(AP_ROOT)/sidecar/attestation docker DEV_REGISTRY=$(DEV_REGISTRY) DEV_COMMIT=$(DEV_COMMIT)
	$(KIND) load docker-image "$(ATTESTATION_SIDECAR_IMAGE)" --name "$(KIND_NAME)"
	@echo "CC setup complete. RuntimeClasses, node labels, and sidecar image loaded."

.PHONY: kube-teardown-cc
kube-teardown-cc:
	-kubectl delete -f cc-setup.yaml 2>/dev/null
	@for node in $$(kubectl get nodes -o name); do \
		kubectl label $$node \
			katacontainers.io/kata-runtime- \
			amd.feature.node.kubernetes.io/snp- \
			intel.feature.node.kubernetes.io/tdx- \
			nvidia.com/cc.ready.state- \
			2>/dev/null; \
	done
	@echo "CC teardown complete."

.PHONY: clean-kube
clean-kube:

.PHONY: kube-deployments-rollout
kube-deployments-rollout: #$(patsubst %, kube-deployment-rollout-%,$(KUSTOMIZE_INSTALLS))

.PHONY: kube-setup-kube
ifeq ($(GATEWAY_API),true)
kube-setup-kube: kube-setup-hostname-operator-gateway
else ifeq ($(CONFIDENTIAL_COMPUTE),true)
kube-setup-kube: kube-setup-cc
else
kube-setup-kube:
endif

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
