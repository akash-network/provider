# KIND_NAME NOTE: 'kind' string literal is default for the GH actions
# KinD, it's fine to use other names locally, however in GH container name
# is configured by engineerd/setup-kind. `kind-control-plane` is the docker
# image's name in GH Actions.
KIND_NAME ?= $(shell basename $$PWD)

export KIND_NAME

ifeq (, $(KINDEST_VERSION))
$(error "KINDEST_VERSION is not set")
endif

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
