HELM_CHARTS ?=

LOKI_VERSION     ?= 2.9.1
PROMTAIL_VERSION ?= 3.11.0
GRAFANA_VERSION  ?= 6.21.2

.PHONY: kind-install-helm-charts
kind-install-helm-charts: $(patsubst %, kind-install-helm-chart-%,$(HELM_CHARTS))

# Create a kubernetes cluster with multi-tenant loki, promtail and grafana integrated for logging.
# See: https://www.scaleway.com/en/docs/tutorials/manage-k8s-logging-loki/ for more info.
.PHONY: kind-install-helm-chart-loki
kind-install-helm-chart-loki:
	helm repo add grafana https://grafana.github.io/helm-charts
	helm repo update
	helm upgrade --install loki grafana/loki \
		--version $(LOKI_VERSION) \
		--create-namespace \
		--namespace loki-stack \
		--set persistence.enabled=true,persistence.size=10Gi,config.auth_enabled=true
	helm upgrade --install promtail grafana/promtail \
		--version $(PROMTAIL_VERSION) \
		--namespace loki-stack \
		-f ../promtail-values.yaml
	helm upgrade --install grafana grafana/grafana \
		--version $(GRAFANA_VERSION) \
		--namespace loki-stack \
		--set persistence.enabled=true,persistence.type=pvc,persistence.size=10Gi
