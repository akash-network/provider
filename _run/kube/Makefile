GATEWAY_API ?= false

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
		--ip-operator=true \
		$(if $(filter true,$(GATEWAY_API)),--ingress-mode=gateway-api --gateway-name=akash-gateway --gateway-namespace=akash-gateway)

.PHONY: provider-lease-ping
provider-lease-ping:
	curl -sIH "Host: hello.localhost" localhost:$(KIND_HTTP_PORT)

.PHONY: hostname-operator
hostname-operator:
	$(PROVIDER_SERVICES) hostname-operator

.PHONY: clean-kube
clean-kube:

.PHONY: kube-deployments-rollout
kube-deployments-rollout: #$(patsubst %, kube-deployment-rollout-%,$(KUSTOMIZE_INSTALLS))

.PHONY: kube-setup-hostname-operator-ingress
kube-setup-hostname-operator-ingress:
	@echo "Configuring hostname operator for NGINX Ingress mode..."
	kubectl wait --for=condition=available deployment/operator-hostname -n akash-services --timeout=90s
	kubectl patch configmap operator-hostname -n akash-services --type=strategic --patch-file hostname-operator-ingress-patch.yaml
	kubectl rollout restart deployment/operator-hostname -n akash-services
	kubectl rollout status deployment/operator-hostname -n akash-services --timeout=60s

.PHONY: kube-setup-kube
ifeq ($(GATEWAY_API),true)
kube-setup-kube: kube-setup-hostname-operator-gateway
else
kube-setup-kube: kube-setup-hostname-operator-ingress
endif

.PHONY: kube-setup-ingress-gateway
kube-setup-ingress-gateway:
	kubectl kustomize "https://github.com/nginx/nginx-gateway-fabric/config/crd/gateway-api/standard?ref=v2.4.1&timeout=90s" | kubectl apply -f -
	helm install ngf oci://ghcr.io/nginx/charts/nginx-gateway-fabric \
		--version 2.4.1 \
		--create-namespace \
		-n nginx-gateway \
		--set nginx.service.type=NodePort \
		--set-json 'nginx.service.nodePorts=[{"port":31437,"listenerPort":80},{"port":31438,"listenerPort":443}]'
	kubectl apply -f "$(METALLB_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_IP_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_SERVICE_PATH)"
	kubectl apply -f gateway-resources.yaml
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
