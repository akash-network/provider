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
kube-deployments-rollout: #$(patsubst %, kube-deployment-rollout-%,$(KUSTOMIZE_INSTALLS))

.PHONY: kube-setup-hostname-operator-ingress
kube-setup-hostname-operator-ingress:
	@echo "Configuring hostname operator for NGINX Ingress mode..."
	kubectl wait --for=condition=available deployment/operator-hostname -n akash-services --timeout=90s
	kubectl patch configmap operator-hostname -n akash-services --type=strategic --patch-file hostname-operator-ingress-patch.yaml
	kubectl rollout restart deployment/operator-hostname -n akash-services
	kubectl rollout status deployment/operator-hostname -n akash-services --timeout=60s

.PHONY: kube-setup-kube
kube-setup-kube: kube-setup-hostname-operator-ingress

.INTERMEDIATE: kube-cluster-create-kind-gateway
kube-cluster-create-kind-gateway: $(KIND)
	$(KIND) create cluster --config kind-config-gateway.yaml --name "$(KIND_NAME)" --image "$(KIND_IMG)"

KUBE_CREATE_GATEWAY := $(AP_RUN_DIR)/.kube-create-gateway

$(KUBE_CREATE_GATEWAY): $(AP_RUN_DIR) kube-cluster-create-kind-gateway
	$(SETUP_KUBE) --crd=$(CRD_FILE) kind init "$(KUBE_SSH_NODES)"
	touch $@

.PHONY: kube-setup-ingress-gateway
kube-setup-ingress-gateway:
	kubectl kustomize "https://github.com/nginx/nginx-gateway-fabric/config/crd/gateway-api/standard?ref=v2.3.0" | kubectl apply -f -
	helm install ngf oci://ghcr.io/nginx/charts/nginx-gateway-fabric \
		--create-namespace \
		-n nginx-gateway \
		--set nginx.service.type=NodePort \
		--set-json 'nginx.service.nodePorts=[{"port":31437,"listenerPort":80},{"port":31438,"listenerPort":443}]'
	kubectl apply -f "$(METALLB_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_IP_CONFIG_PATH)"
	kubectl apply -f "$(METALLB_SERVICE_PATH)"
	kubectl apply -f gateway-resources.yaml
	@echo "Waiting for Gateway to be ready..."
	@kubectl wait --for=condition=Programmed gateway/akash-gateway -n akash-gateway --timeout=120s || true

.PHONY: kube-setup-hostname-operator-gateway
kube-setup-hostname-operator-gateway:
	@echo "Configuring hostname operator for Gateway API mode..."
	kubectl wait --for=condition=available deployment/operator-hostname -n akash-services --timeout=90s
	kubectl patch configmap operator-hostname -n akash-services --type=strategic --patch-file hostname-operator-gateway-patch.yaml
	kubectl rollout restart deployment/operator-hostname -n akash-services
	kubectl rollout status deployment/operator-hostname -n akash-services --timeout=60s

.PHONY: kube-cluster-setup-gateway
kube-cluster-setup-gateway: init \
	kube-prepare-images \
	$(KUBE_CREATE_GATEWAY) \
	kube-cluster-check-info \
	kube-setup-ingress-gateway \
	kube-upload-images \
	kustomize-init \
	kustomize-deploy-services \
	kube-deployments-rollout \
	kube-install-helm-charts \
	kube-setup-$(AP_RUN_NAME) \
	kube-setup-hostname-operator-gateway

.PHONY: provider-run-gateway
provider-run-gateway:
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
		--ingress-mode=gateway-api \
		--gateway-name=akash-gateway \
		--gateway-namespace=akash-gateway

# Override the standard kube-cluster-delete-kind to handle both marker files
.PHONY: kube-cluster-delete
kube-cluster-delete: $(KIND)
	$(KIND) delete cluster --name "$(KIND_NAME)"
	rm -rf $(KUBE_CREATE) $(KUBE_CREATE_GATEWAY)
