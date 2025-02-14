# KIND_NAME NOTE: 'kind' string literal is default for the GH actions 
# KinD, it's fine to use other names locally, however in GH container name 
# is configured by engineerd/setup-kind. `kind-control-plane` is the docker
# image's name in GH Actions.
include $(AP_ROOT)/_run/common-kind-vars.mk

KIND_IMG         ?= kindest/node:$(KINDEST_VERSION)
K8S_CONTEXT      ?= $(shell kubectl config current-context)
SETUP_KIND       := $(AP_ROOT)/script/setup-kind.sh
KIND_CONFIG      ?= default
KIND_CONFIG_FILE := $(shell "$(SETUP_KIND)" config-file $$PWD $(KIND_CONFIG))

.PHONY: app-http-port
app-http-port:
	@echo $(KIND_HTTP_PORT)

.PHONY: kind-k8s-ip
kind-k8s-ip:
	@echo $(KIND_K8S_IP)

.PHONY: kind-port-bindings
kind-port-bindings: $(KIND)
	@echo $(KIND_PORT_BINDINGS)

.INTERMEDIATE: kube-cluster-create-kind
kube-cluster-create-kind: $(KIND)
	$(KIND) create cluster --config "$(KIND_CONFIG_FILE)" --name "$(KIND_NAME)" --image "$(KIND_IMG)"

.PHONY: kube-cluster-delete-kind
kube-cluster-delete-kind: $(KIND)
	$(KIND) delete cluster --name "$(KIND_NAME)"
	rm -rf $(KUBE_CREATE)
