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
# Import key. AKASH_FROM contains key name
##
/bin/akash keys import "$AKASH_FROM" "$AKASH_BOOT_KEYS/key.txt" < "$AKASH_BOOT_KEYS/key-pass.txt"
