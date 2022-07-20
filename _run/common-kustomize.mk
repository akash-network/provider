KUSTOMIZE_ROOT                 ?= $(AP_ROOT)/_docs/kustomize
KUSTOMIZE_DIR                  := $(DEVCACHE_RUN)/$(KIND_NAME)/kustomize
KUSTOMIZE_PROVIDER             := $(KUSTOMIZE_DIR)/akash-provider
KUSTOMIZE_AKASH                := $(KUSTOMIZE_DIR)/akash-node
KUSTOMIZE_OPERATOR_HOSTNAME    := $(KUSTOMIZE_DIR)/akash-operator-hostname
KUSTOMIZE_OPERATOR_INVENTORY   := $(KUSTOMIZE_DIR)/akash-operator-inventory
KUSTOMIZE_OPERATOR_IP          := $(KUSTOMIZE_DIR)/akash-operator-ip

CLIENT_EXPORT_PASSWORD         ?= 12345678

$(KUSTOMIZE_DIR):
	mkdir -p $(KUSTOMIZE_DIR)

.PHONY: kustomize-init
kustomize-init: $(KUSTOMIZE_DIR) kustomize-templates kustomize-set-images kustomize-configure-services

#### Kustomize init templates
.PHONY: kustomize-templates
kustomize-templates: $(patsubst %, kustomize-template-%,$(KUSTOMIZE_INSTALLS))

.PHONY: kustomize-template-%
kustomize-template-%:
	cp -r $(ROOT_DIR)/_docs/kustomize/templates/$* $(KUSTOMIZE_DIR)/


#### Kustomize configure images
.PHONY: kustomize-set-images
kustomize-set-images: $(patsubst %, kustomize-set-image-%,$(KUSTOMIZE_INSTALLS))

.PHONY: kustomize-set-image-akash-node
kustomize-set-image-akash-node:
	echo "- op: replace\n  path: /spec/template/spec/containers/0/image\n  value: $(AKASH_DOCKER_IMAGE)" > $(KUSTOMIZE_DIR)/akash-node/docker-image.yaml

.PHONY: kustomize-set-image-akash-provider
kustomize-set-image-akash-provider:
	echo "- op: replace\n  path: /spec/template/spec/initContainers/0/image\n  value: $(AKASH_DOCKER_IMAGE)" > $(KUSTOMIZE_DIR)/akash-provider/docker-image.yaml
	echo "- op: replace\n  path: /spec/template/spec/containers/0/image\n  value: $(DOCKER_IMAGE)" >> $(KUSTOMIZE_DIR)/akash-provider/docker-image.yaml

.PHONY: kustomize-set-image-akash-operator-%
kustomize-set-image-akash-operator-%:
	echo "- op: replace\n  path: /spec/template/spec/containers/0/image\n  value: $(DOCKER_IMAGE)" > "$(KUSTOMIZE_DIR)/akash-operator-$*/docker-image.yaml"

#### Kustomize configurations
.PHONY: kustomize-configure-services
kustomize-configure-services: $(patsubst %, kustomize-configure-%,$(KUSTOMIZE_INSTALLS))

.PHONY: kustomize-configure-akash-node
kustomize-configure-akash-node:
	mkdir -p "$(KUSTOMIZE_AKASH)/cache"
	cp -r "$(AKASH_HOME)/"* "$(KUSTOMIZE_AKASH)/cache/"

.PHONY: kustomize-configure-akash-provider
kustomize-configure-akash-provider:
	mkdir -p "$(KUSTOMIZE_PROVIDER)/cache"
	cp -r "$(AKASH_HOME)/config" "$(KUSTOMIZE_PROVIDER)/cache/"
	echo "$(CLIENT_EXPORT_PASSWORD)" > "$(KUSTOMIZE_PROVIDER)/cache/key-pass.txt"
	cat "$(AKASH_HOME)/$(PROVIDER_ADDRESS).pem" > "$(KUSTOMIZE_PROVIDER)/cache/provider-cert.pem"
	( \
		cat "$(KUSTOMIZE_PROVIDER)/cache/key-pass.txt" ; \
		cat "$(KUSTOMIZE_PROVIDER)/cache/key-pass.txt"   \
	) | $(AKASH) keys export provider 1> "$(KUSTOMIZE_PROVIDER)/cache/key.txt"

.PHONY: kustomize-configure-akash-operator-hostname
kustomize-configure-akash-operator-hostname:

.PHONY: kustomize-configure-akash-operator-ip
kustomize-init-akash-operator-ip:

.PHONY: kustomize-configure-akash-operator-inventory
kustomize-init-configure-operator-inventory:

#### Kustomize installations
.PHONY: kustomize-deploy-services
kustomize-deploy-services: $(patsubst %, kustomize-deploy-%,$(KUSTOMIZE_INSTALLS))

.PHONY: kustomize-deploy-%
kustomize-deploy-%:
	kubectl kustomize $(KUSTOMIZE_DIR)/$* | kubectl apply -f-

.PHONY: clean-kustomize
clean-kustomize:
	rm -rf $(KUSTOMIZE_DIR)
