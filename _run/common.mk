OPTIONS ?=

SKIP_BUILD := false

# check for nostrip option
ifneq (,$(findstring nobuild,$(OPTIONS)))
	SKIP_BUILD := true
endif

include ../common-base.mk

# https://stackoverflow.com/a/7531247
# https://www.gnu.org/software/make/manual/make.html#Flavors
null  := 
space := $(null) #
comma := ,

export AKASH_KEYRING_BACKEND    = test
export AKASH_GAS_ADJUSTMENT     = 2
export AKASH_CHAIN_ID           = local
export AKASH_YES                = true
export AKASH_GAS_PRICES         = 0.025uakt
export AKASH_GAS                = auto
export AKASH_NODE               = http://localhost:26657

export AP_HOME                  = $(AKASH_HOME)
export AP_KEYRING_BACKEND       = $(AKASH_KEYRING_BACKEND)
export AP_GAS_ADJUSTMENT        = $(AKASH_GAS_ADJUSTMENT)
export AP_CHAIN_ID              = $(AKASH_CHAIN_ID)
export AP_YES                   = $(AKASH_YES)
export AP_GAS_PRICES            = $(AKASH_GAS_PRICES)
export AP_GAS                   = $(AKASH_GAS)
export AP_NODE                  = $(AKASH_NODE)

AKASH_INIT                     := $(AP_RUN_DIR)/.akash-init

GENESIS_PATH := $(AKASH_HOME)/config/genesis.json

CHAIN_MIN_DEPOSIT        := 10000000000000
CHAIN_ACCOUNT_DEPOSIT    := $(shell echo $$(($(CHAIN_MIN_DEPOSIT) * 10)))
CHAIN_VALIDATOR_DELEGATE := $(shell echo $$(($(CHAIN_MIN_DEPOSIT) / 2)))
CHAIN_TOKEN_DENOM        := uakt
CHAIN_DEPOSIT_DENOM      := uact

KEY_NAMES := main provider validator other

MULTISIG_KEY     := msig
MULTISIG_SIGNERS := main other

GENESIS_ACCOUNTS := $(KEY_NAMES) $(MULTISIG_KEY)

CLIENT_CERTS := main validator other
SERVER_CERTS := provider

# akt keeps its config, contexts and keyring in AKT_HOME (= AKASH_HOME, see
# common-base.mk). Without a config akt starts an interactive first-run wizard,
# so nothing below runs akt before the config exists: client-init-context
# creates it with "context network create", which akt exempts from the wizard.
AKT_CONFIG   := $(AKT_HOME)/config.yaml

# akt_key_address <key-name>: bech32 address of a key, or empty while akt is
# not initialised. Tested with [ -f ] at expansion time rather than $(wildcard),
# which may serve a directory listing cached before the config was created.
akt_key_address = $(shell [ -f "$(AKT_CONFIG)" ] && $(AKT) context keys show "$(1)" -a </dev/null)

.PHONY: akt-context-ready
akt-context-ready:
	@test -f "$(AKT_CONFIG)" || { echo 'akt is not initialised in $(AKT_HOME); run "make init"' >&2; exit 1; }

.PHONY: init
init: bins akash-init

$(AP_RUN_DIR):
	mkdir -p $@

$(AKASH_HOME):
	mkdir -p $@

# AKASH_HOME is order-only: a running node writes into it, which must not make a
# completed init look stale.
$(AKASH_INIT): client-init node-init | $(AKASH_HOME)
	touch $@

.INTERMEDIATE: akash-init
akash-init: $(AKASH_INIT)

.INTERMEDIATE: client-init
client-init: akash-init-reset client-init-context client-init-keys

# An init that did not finish leaves keys, certificates and genesis state that
# its steps cannot resume (they refuse to overwrite them). Without the
# completion marker, start over from an empty AKASH_HOME (= AKT_HOME).
.INTERMEDIATE: akash-init-reset
akash-init-reset:
	@if [ ! -f "$(AKASH_INIT)" ] && [ -d "$(AKASH_HOME)" ] && [ -n "$$(ls -A "$(AKASH_HOME)")" ]; then \
		echo "$(AKASH_HOME) holds an unfinished init; starting it over"; \
		find "$(AKASH_HOME)" -mindepth 1 -delete; \
	fi

# Reconciles the akt network, keyring and context named "local" one by one from
# their own state, so a run interrupted after any step converges when re-run.
# Definitions that exist but differ from this environment are not overwritten.
.INTERMEDIATE: client-init-context
client-init-context:
	@set -e; \
	if [ -f "$(AKT_CONFIG)" ] && $(AKT) context network show local >/dev/null 2>&1 </dev/null; then \
		echo "akt: network local exists"; \
	else \
		$(AKT) context network create local \
			--chain-id "$(AKASH_CHAIN_ID)" \
			--rpc "$(AKASH_NODE)" \
			--gas-prices "$(AKASH_GAS_PRICES)" \
			--gas-adjustment "$(AKASH_GAS_ADJUSTMENT)" </dev/null; \
	fi; \
	keyring=$$($(AKT) context keyring list -o json </dev/null | jq -c '.[] | select(.name == "local")'); \
	if [ -z "$$keyring" ]; then \
		$(AKT) context keyring create local "$(AKASH_KEYRING_BACKEND)" --dir "$(AKASH_HOME)" </dev/null; \
	elif ! jq -e --arg backend "$(AKASH_KEYRING_BACKEND)" --arg dir "$(AKASH_HOME)" \
		'.backend == $$backend and .dir == $$dir' <<<"$$keyring" >/dev/null; then \
		echo "akt keyring local in $(AKT_HOME) does not match this environment: $$keyring; run \"make clean\"" >&2; \
		exit 1; \
	fi; \
	context=$$($(AKT) context list -o json </dev/null | jq -c '.[] | select(.name == "local")'); \
	if [ -z "$$context" ]; then \
		$(AKT) context create local --network local --keyring local --default-account "$(KEY_NAME)" </dev/null; \
	elif ! jq -e '.network == "local" and .keyring == "local"' <<<"$$context" >/dev/null; then \
		echo "akt context local in $(AKT_HOME) does not match this environment: $$context; run \"make clean\"" >&2; \
		exit 1; \
	fi; \
	$(AKT) context use local </dev/null

.INTERMEDIATE: client-init-keys
client-init-keys: $(patsubst %,client-init-key-%,$(KEY_NAMES)) client-init-key-multisig

.INTERMEDIATE: $(patsubst %,client-init-key-%,$(KEY_NAMES))
client-init-key-%:
	$(AKT) context keys add "$(@:client-init-key-%=%)" --no-backup </dev/null

.INTERMEDIATE: client-init-key-multisig
client-init-key-multisig:
	$(AKT) context keys add \
		"$(MULTISIG_KEY)" \
		--multisig "$(subst $(space),$(comma),$(strip $(MULTISIG_SIGNERS)))" \
		--multisig-threshold 2 </dev/null

.INTERMEDIATE: node-init
node-init: node-init-genesis node-init-genesis-accounts node-init-genesis-certs node-init-gentx node-init-finalize

.INTERMEDIATE: node-init-genesis
node-init-genesis:
	$(AKASH) genesis init node0
	cp "$(GENESIS_PATH)" "$(GENESIS_PATH).orig"
	cat "$(GENESIS_PATH).orig" | \
		jq -M '.app_state.gov.voting_params.voting_period = "30s"' | \
		jq -rM '(..|objects|select(has("bond_denom"))).bond_denom |= "$(CHAIN_TOKEN_DENOM)"' | \
		jq -rM '(..|objects|select(has("mint_denom"))).mint_denom |= "$(CHAIN_TOKEN_DENOM)"' > \
		"$(GENESIS_PATH)"

.INTERMEDIATE: node-init-genesis-certs
node-init-genesis-certs: $(patsubst %,node-init-genesis-client-cert-%,$(CLIENT_CERTS)) $(patsubst %,node-init-genesis-server-cert-%,$(SERVER_CERTS))

.INTERMEDIATE: $(patsubst %,node-init-genesis-client-cert-%,$(CLIENT_CERTS))
node-init-genesis-client-cert-%:
	$(AKT) tx cert generate client --from=$* </dev/null
	$(AKT) tx cert publish client --to-genesis --from=$* </dev/null

.INTERMEDIATE: $(patsubst %,node-init-genesis-server-cert-%,$(SERVER_CERTS))
node-init-genesis-server-cert-%:
	$(AKT) tx cert generate server localhost akash-provider.localhost --from=$* </dev/null
	$(AKT) tx cert publish server --to-genesis --from=$* </dev/null

.INTERMEDIATE: node-init-genesis-accounts
node-init-genesis-accounts: $(patsubst %,node-init-genesis-account-%,$(GENESIS_ACCOUNTS))
	$(AKASH) genesis validate

.INTERMEDIATE: $(patsubst %,node-init-genesis-account-%,$(GENESIS_ACCOUNTS))
node-init-genesis-account-%:
	$(AKASH) genesis add-account \
		"$(call akt_key_address,$(@:node-init-genesis-account-%=%))" \
		"$(CHAIN_MIN_DEPOSIT)$(CHAIN_DEPOSIT_DENOM),$(CHAIN_MIN_DEPOSIT)$(CHAIN_TOKEN_DENOM)"

.INTERMEDIATE: node-init-gentx
node-init-gentx:
	$(AKASH) genesis gentx validator "$(CHAIN_VALIDATOR_DELEGATE)$(CHAIN_TOKEN_DENOM)" --min-self-delegation=1 --gas=auto --gas-prices=0.025uakt

.INTERMEDIATE: node-init-finalize
node-init-finalize:
	$(AKASH) genesis collect
	$(AKASH) genesis validate

.PHONY: node-run
node-run:
	$(AKASH) start --minimum-gas-prices=$(AKASH_GAS_PRICES)

.PHONY: node-status
node-status:
	$(AKASH) status

.PHONY: rest-server-run
rest-server-run:
	$(AKASH) rest-server

.PHONY: clean
clean: clean-$(AP_RUN_NAME)
	rm -rf "$(DEVCACHE_RUN)/$(AP_RUN_NAME)"

.PHONY: rosetta-run
rosetta-run:
	$(AKASH) rosetta --addr localhost:8080 --grpc localhost:9090 --network=$(AKASH_CHAIN_ID) --blockchain=akash
