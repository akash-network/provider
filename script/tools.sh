#!/usr/bin/env bash

set -x

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
SEMVER=$SCRIPT_DIR/semver.sh

gomod="$SCRIPT_DIR/../go.mod"

function get_gotoolchain() {
    local gotoolchain
    local goversion

    gotoolchain=$(grep -E '^toolchain go[0-9]{1,}.[0-9]{1,}.[0-9]{1,}$' < "$gomod" | cut -d ' ' -f 2 | tr -d '\n')

    if [[ ${gotoolchain} == "" ]]; then
        # determine go toolchain from go version in go.mod
        if which go > /dev/null 2>&1 ; then
            goversion=$(GOTOOLCHAIN=local go version | cut -d ' ' -f 3 | sed 's/go*//' | tr -d '\n')
        fi

        if [[ $goversion != "" ]] && [[ $($SEMVER compare "v$goversion" v1.21.0) -ge 0 ]]; then
            gotoolchain=go${goversion}
        else
            gotoolchain=go$(grep -E '^go [0-9]{1,}.[0-9]{1,}$' < "$gomod" | cut -d ' ' -f 2 | tr -d '\n').0
        fi
    fi

    echo -n "$gotoolchain"
}

function build_akash() {
    dev_cache=${AP_DEVCACHE_BIN}
    cd "$1" || exit 1
    export AKASH_ROOT="$1"
    source .env
    make akash AKASH="${dev_cache}/akash"
}

function build_akash_docker() {
    cd "$1" || exit 1
    export AKASH_ROOT="$1"
    source .env
    make docker-image
}

case "$1" in
gotoolchain)
    get_gotoolchain
    ;;
build-akash)
    shift
    build_akash "$@"
    ;;
build-akash-docker)
    shift
    build_akash_docker "$@"
    ;;
esac
