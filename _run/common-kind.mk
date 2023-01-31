# KIND_NAME NOTE: 'kind' string literal is default for the GH actions 
# KinD, it's fine to use other names locally, however in GH container name 
# is configured by engineerd/setup-kind. `kind-control-plane` is the docker
# image's name in GH Actions.
export KIND_NAME ?= $(shell basename $$PWD)

ifeq (, $(KINDEST_VERSION))
$(error "KINDEST_VERSION is not set")
endif

KIND_IMG         ?= kindest/node:$(KINDEST_VERSION)
K8S_CONTEXT      ?= $(shell kubectl config current-context)
SETUP_KIND       := "$(AP_ROOT)/script/setup-kind.sh"
KIND_CONFIG      ?= default
KIND_CONFIG_FILE := $(shell "$(SETUP_KIND)" config-file $$PWD $(KIND_CONFIG))

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
kube-cluster-create-kind: $(KUBE_CREATE)

.PHONY: kube-cluster-delete-kind
kube-cluster-delete-kind: $(KIND)
	$(KIND) delete cluster --name "$(KIND_NAME)"
	rm -rf $(KUBE_CREATE)
