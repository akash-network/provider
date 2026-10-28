include ../common-gateway.mk

MINIKUBE_VM_DRIVER ?=
MINIKUBE_IP         = $(shell minikube ip)
MINIKUBE_INVOKE     = VM_DRIVER=$(MINIKUBE_VM_DRIVER) ROOK_PATH=$(AP_ROOT)/_docs/rook/test $(AP_ROOT)/script/setup-minikube.sh

.PHONY: minikube-cluster-create
minikube-cluster-create:
	$(MINIKUBE_INVOKE) up
	$(MINIKUBE_INVOKE) akash-setup
	$(MAKE) kube-setup-gateway
	$(MINIKUBE_INVOKE) deploy-rook

.PHONY: minikube-cluster-delete
minikube-cluster-delete:
	$(MINIKUBE_INVOKE) clean

.PHONY: ip
ip:
	@echo $(MINIKUBE_IP)
