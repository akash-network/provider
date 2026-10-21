KEY_NAME ?= main

KEY_ADDRESS          ?= $(call akt_key_address,$(KEY_NAME))
PROVIDER_KEY_NAME    ?= provider
PROVIDER_ADDRESS     ?= $(call akt_key_address,$(PROVIDER_KEY_NAME))
PROVIDER_CONFIG_PATH ?= provider.yaml

SDL_PATH ?= deployment.yaml

DSEQ           ?= 1
GSEQ           ?= 1
OSEQ           ?= 1
PRICE          ?= 10uakt
CERT_HOSTNAME  ?= localhost
LEASE_SERVICES ?= web

# Deployment deposit in uact, e.g. DEPOSIT=5000000uact. Empty lets akt use the
# chain's minimum uact deposit. SDLs priced in uakt cannot be deployed on the
# current chain (see _run/kube/README.md).
DEPOSIT        ?=

GW_AUTH_TYPE   ?= jwt

# Provider gateway commands have no --from: they act as the context's default
# account unless AKT_FROM selects another key, so pass the tenant explicitly.
AKT_AS_TENANT   = AKT_FROM="$(KEY_NAME)" $(AKT)

.PHONY: multisig-send
multisig-send: akt-context-ready
	$(AKT) tx bank send \
		"$(MULTISIG_KEY)" \
		"$(KEY_ADDRESS)" \
		1000000uakt \
		--generate-only \
		-o json \
		> "$(AKASH_HOME)/multisig-tx.json"
	$(AKT) tx sign \
		"$(AKASH_HOME)/multisig-tx.json" \
		--multisig "$(call akt_key_address,$(MULTISIG_KEY))" \
		--from "main" \
		--sign-mode amino-json \
		--output-document "$(AKASH_HOME)/multisig-sig-main.json"
	$(AKT) tx sign \
		"$(AKASH_HOME)/multisig-tx.json" \
		--multisig "$(call akt_key_address,$(MULTISIG_KEY))" \
		--from "other" \
		--sign-mode amino-json \
		--output-document "$(AKASH_HOME)/multisig-sig-other.json"
	$(AKT) tx multi-sign \
		"$(AKASH_HOME)/multisig-tx.json" \
		"$(MULTISIG_KEY)" \
		"$(AKASH_HOME)/multisig-sig-main.json" \
		"$(AKASH_HOME)/multisig-sig-other.json" \
		--output-document "$(AKASH_HOME)/multisig-final.json"
	$(AKT) tx broadcast "$(AKASH_HOME)/multisig-final.json" -y

.PHONY: provider-create
provider-create: akt-context-ready
	$(AKT) tx provider create "$(PROVIDER_CONFIG_PATH)" --from "$(PROVIDER_KEY_NAME)" -y

.PHONY: provider-update
provider-update: akt-context-ready
	$(AKT) tx provider update "$(PROVIDER_CONFIG_PATH)" --from "$(PROVIDER_KEY_NAME)" -y

.PHONY: provider-status
provider-status: akt-context-ready
	$(AKT) provider status "$(PROVIDER_ADDRESS)"

.PHONY: send-manifest
send-manifest: akt-context-ready
	$(AKT_AS_TENANT) provider send-manifest "$(SDL_PATH)" \
		--dseq      "$(DSEQ)" \
		--provider  "$(PROVIDER_ADDRESS)" \
		--auth-type "$(GW_AUTH_TYPE)"

.PHONY: get-manifest
get-manifest: akt-context-ready
	$(AKT_AS_TENANT) provider get-manifest "$(DSEQ)" \
		--provider  "$(PROVIDER_ADDRESS)" \
		--auth-type "$(GW_AUTH_TYPE)"

.PHONY: deployment-create
deployment-create: akt-context-ready
	$(AKT) tx deployment create "$(SDL_PATH)" \
		--dseq "$(DSEQ)" \
		$(if $(DEPOSIT),--deposit "$(DEPOSIT)") \
		--from "$(KEY_NAME)" \
		-y

.PHONY: deployment-deposit
deployment-deposit: akt-context-ready
	$(AKT) tx escrow deposit deployment "$(PRICE)" \
		--dseq "$(DSEQ)" \
		--from "$(KEY_NAME)" \
		-y

.PHONY: deployment-update
deployment-update: akt-context-ready
	$(AKT) tx deployment update "$(SDL_PATH)" "$(DSEQ)" \
		--from "$(KEY_NAME)" \
		-y

.PHONY: deployment-close
deployment-close: akt-context-ready
	$(AKT) tx deployment close "$(DSEQ)" \
		--from "$(KEY_NAME)" \
		-y

.PHONY: group-close
group-close: akt-context-ready
	$(AKT) tx deployment group close "$(DSEQ)" "$(GSEQ)" \
		--from "$(KEY_NAME)" \
		-y

.PHONY: group-pause
group-pause: akt-context-ready
	$(AKT) tx deployment group pause "$(DSEQ)" "$(GSEQ)" \
		--from "$(KEY_NAME)" \
		-y

.PHONY: group-start
group-start: akt-context-ready
	$(AKT) tx deployment group start "$(DSEQ)" "$(GSEQ)" \
		--from "$(KEY_NAME)" \
		-y

.PHONY: bid-create
bid-create: akt-context-ready
	$(AKT) tx market bid create \
		--owner "$(KEY_ADDRESS)" \
		--dseq  "$(DSEQ)" \
		--gseq  "$(GSEQ)" \
		--oseq  "$(OSEQ)" \
		--price "$(PRICE)" \
		--from  "$(PROVIDER_KEY_NAME)" \
		-y

.PHONY: bid-close
bid-close: akt-context-ready
	$(AKT) tx market bid close \
		--owner "$(KEY_ADDRESS)" \
		--dseq  "$(DSEQ)" \
		--gseq  "$(GSEQ)" \
		--oseq  "$(OSEQ)" \
		--from  "$(PROVIDER_KEY_NAME)" \
		-y

.PHONY: lease-create
lease-create: akt-context-ready
	$(AKT) tx market lease create "$(DSEQ)" "$(PROVIDER_ADDRESS)" \
		--gseq "$(GSEQ)" \
		--oseq "$(OSEQ)" \
		--from "$(KEY_NAME)" \
		-y

.PHONY: lease-withdraw
lease-withdraw: akt-context-ready
	$(AKT) tx market lease withdraw "$(DSEQ)" "$(PROVIDER_ADDRESS)" \
		--owner "$(KEY_ADDRESS)" \
		--gseq  "$(GSEQ)" \
		--oseq  "$(OSEQ)" \
		--from  "$(PROVIDER_KEY_NAME)" \
		-y

.PHONY: lease-close
lease-close: akt-context-ready
	$(AKT) tx market lease close "$(DSEQ)" "$(PROVIDER_ADDRESS)" \
		--gseq "$(GSEQ)" \
		--oseq "$(OSEQ)" \
		--from "$(KEY_NAME)" \
		-y

.PHONY: query-accounts
query-accounts: $(patsubst %, query-account-%,$(GENESIS_ACCOUNTS))

.PHONY: query-account-%
query-account-%: akt-context-ready
	$(AKT) query bank balances "$(call akt_key_address,$*)"
	$(AKT) query auth account  "$(call akt_key_address,$*)"

.PHONY: query-provider
query-provider: akt-context-ready
	$(AKT) query provider "$(PROVIDER_ADDRESS)"

.PHONY: query-providers
query-providers: akt-context-ready
	$(AKT) query provider

.PHONY: query-deployment
query-deployment: akt-context-ready
	$(AKT) query deployment "$(KEY_ADDRESS)/$(DSEQ)"

.PHONY: query-deployments
query-deployments: akt-context-ready
	$(AKT) query deployment "$(KEY_ADDRESS)"

.PHONY: query-order
query-order: akt-context-ready
	$(AKT) query market order "$(KEY_ADDRESS)/$(DSEQ)/$(GSEQ)/$(OSEQ)"

.PHONY: query-orders
query-orders: akt-context-ready
	$(AKT) query market order "$(KEY_ADDRESS)"

.PHONY: query-bid
query-bid: akt-context-ready
	$(AKT) query market bid "$(KEY_ADDRESS)/$(DSEQ)/$(GSEQ)/$(OSEQ)/$(PROVIDER_ADDRESS)"

.PHONY: query-bids
query-bids: akt-context-ready
	$(AKT) query market bid "$(KEY_ADDRESS)"

.PHONY: query-lease
query-lease: akt-context-ready
	$(AKT) query market lease "$(KEY_ADDRESS)/$(DSEQ)/$(GSEQ)/$(OSEQ)/$(PROVIDER_ADDRESS)"

.PHONY: query-leases
query-leases: akt-context-ready
	$(AKT) query market lease "$(KEY_ADDRESS)"

.PHONY: query-certificates
query-certificates: akt-context-ready
	$(AKT) query cert list "$(KEY_ADDRESS)"

.PHONY: query-account-certificates
query-account-certificates: akt-context-ready
	$(AKT) query cert list "$(KEY_ADDRESS)" valid

.PHONY: create-server-certificate
create-server-certificate: akt-context-ready
	$(AKT) tx cert generate server "$(CERT_HOSTNAME)" --from "$(KEY_NAME)" --overwrite
	$(AKT) tx cert publish server --from "$(KEY_NAME)" -y

.PHONY: revoke-certificate
revoke-certificate: akt-context-ready
	$(AKT) tx cert revoke client --from "$(KEY_NAME)" -y

.PHONY: provider-lease-logs
provider-lease-logs: akt-context-ready
	$(AKT_AS_TENANT) provider lease-logs "$(DSEQ)" \
		-f \
		--service   "$(LEASE_SERVICES)" \
		--provider  "$(PROVIDER_ADDRESS)" \
		--auth-type "$(GW_AUTH_TYPE)"

.PHONY: provider-lease-events
provider-lease-events: akt-context-ready
	$(AKT_AS_TENANT) provider lease-events "$(DSEQ)" \
		-f \
		--provider  "$(PROVIDER_ADDRESS)" \
		--auth-type "$(GW_AUTH_TYPE)"

.PHONY: provider-lease-attestation
provider-lease-attestation: akt-context-ready
	$(AKT_AS_TENANT) provider lease-attestation "$(DSEQ)" \
		--gseq      "$(GSEQ)" \
		--oseq      "$(OSEQ)" \
		--provider  "$(PROVIDER_ADDRESS)" \
		--auth-type "$(GW_AUTH_TYPE)"

.PHONY: provider-lease-status
provider-lease-status: akt-context-ready
	$(AKT_AS_TENANT) provider lease-status "$(DSEQ)" \
		--gseq      "$(GSEQ)" \
		--oseq      "$(OSEQ)" \
		--provider  "$(PROVIDER_ADDRESS)" \
		--auth-type "$(GW_AUTH_TYPE)"

.PHONY: provider-service-status
provider-service-status: provider-lease-status
