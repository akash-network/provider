#!/bin/sh

set -xe

##
# Configuration sanity check
##

# shellcheck disable=SC2015
[ -f "$AKASH_BOOT_KEYS/key.txt" ] && [ -f "$AKASH_BOOT_KEYS/key-pass.txt" ] || {
    echo "Key information not found; AKASH_BOOT_KEYS is not configured properly"
    exit 1
}

env | sort

##
# akt context for the provider key in AKT_HOME, which provider-services shares
# as AP_HOME. Network, keyring and context are reconciled from their own state,
# so a re-run on the same volume, also after a crash between steps, converges.
# The network is created first: it is the only akt command that runs without a
# config, and it writes that config.
##
if [ -f "$AKT_HOME/config.yaml" ] && akt context network show provider >/dev/null 2>&1 </dev/null; then
    echo "akt: network provider exists"
else
    akt context network create provider --chain-id "$AKASH_CHAIN_ID" --rpc "$AKASH_NODE" </dev/null
fi

keyring=$(akt context keyring list -o json </dev/null | jq -c '.[] | select(.name == "provider")')
if [ -z "$keyring" ]; then
    akt context keyring create provider "$AKASH_KEYRING_BACKEND" --dir "$AKT_HOME" </dev/null
elif ! echo "$keyring" | jq -e --arg backend "$AKASH_KEYRING_BACKEND" --arg dir "$AKT_HOME" \
    '.backend == $backend and .dir == $dir' >/dev/null; then
    echo "akt keyring provider in $AKT_HOME does not match this deployment: $keyring" >&2
    exit 1
fi

context=$(akt context list -o json </dev/null | jq -c '.[] | select(.name == "provider")')
if [ -z "$context" ]; then
    akt context create provider --network provider --keyring provider --default-account "$AKASH_FROM" </dev/null
elif ! echo "$context" | jq -e '.network == "provider" and .keyring == "provider"' >/dev/null; then
    echo "akt context provider in $AKT_HOME does not match this deployment: $context" >&2
    exit 1
fi

akt context use provider </dev/null

##
# Import key. AKASH_FROM contains key name
##
if ! akt context keys show "$AKASH_FROM" -a >/dev/null 2>&1 </dev/null; then
    akt context keys import "$AKASH_FROM" "$AKASH_BOOT_KEYS/key.txt" < "$AKASH_BOOT_KEYS/key-pass.txt"
fi
