# Gateway API data plane for the shared akash-gateway Gateway.
# GATEWAY_IMPL selects the implementation: envoy (default) or nginx (NGINX Gateway
# Fabric, kept for one release as the rollback path).
GATEWAY_IMPL            ?= envoy
GATEWAY_KUSTOMIZE_ROOT  ?= $(AP_ROOT)/_docs/kustomize
GATEWAY_ROLLOUT_TIMEOUT ?= 300

.PHONY: kube-setup-gateway
kube-setup-gateway: kube-setup-gateway-$(GATEWAY_IMPL)

# Envoy Gateway in GatewayNamespace mode, so the proxies run in akash-gateway, the
# namespace tenant NetworkPolicies allow. The controller manifest (pinned in
# envoy-gateway/controller) includes the Gateway API (experimental channel) and
# Envoy Gateway CRDs; several exceed the client-side apply annotation limit.
.PHONY: kube-setup-gateway-envoy
kube-setup-gateway-envoy:
	kubectl kustomize "$(GATEWAY_KUSTOMIZE_ROOT)/envoy-gateway/controller" | kubectl apply --server-side -f -
	kubectl wait --for=condition=Established crd/envoyproxies.gateway.envoyproxy.io crd/gateways.gateway.networking.k8s.io --timeout=$(GATEWAY_ROLLOUT_TIMEOUT)s
	kubectl -n envoy-gateway-system rollout status deployment/envoy-gateway --timeout=$(GATEWAY_ROLLOUT_TIMEOUT)s
	kubectl apply -k "$(GATEWAY_KUSTOMIZE_ROOT)/envoy-gateway"
	kubectl wait --for=condition=Programmed gateway/akash-gateway -n akash-gateway --timeout=$(GATEWAY_ROLLOUT_TIMEOUT)s

.PHONY: kube-setup-gateway-nginx
kube-setup-gateway-nginx:
	kubectl kustomize "$(GATEWAY_KUSTOMIZE_ROOT)/nginx-gateway-fabric/crds" | kubectl apply --server-side -f -
	kubectl kustomize "$(GATEWAY_KUSTOMIZE_ROOT)/nginx-gateway-fabric" | kubectl apply -f -
	kubectl wait --for=condition=Programmed gateway/akash-gateway -n akash-gateway --timeout=$(GATEWAY_ROLLOUT_TIMEOUT)s
