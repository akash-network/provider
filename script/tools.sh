#!/usr/bin/env bash

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
SEMVER=$SCRIPT_DIR/semver.sh

source "$SCRIPT_DIR/semver_funcs.sh"

gomod="$SCRIPT_DIR/../go.mod"

function get_gotoolchain() {
	local gotoolchain
	local goversion
	local local_goversion

	gotoolchain=$(grep -E '^toolchain go[0-9]{1,}.[0-9]{1,}.[0-9]{1,}$' <"$gomod" | cut -d ' ' -f 2 | tr -d '\n')
	goversion=$(grep -E '^go [0-9]{1,}.[0-9]{1,}(.[0-9]{1,})?$' <"$gomod" | cut -d ' ' -f 2 | tr -d '\n')

	if [[ ${gotoolchain} == "" ]]; then
		# determine go toolchain from go version in go.mod
		if which go >/dev/null 2>&1; then
			local_goversion=$(GOTOOLCHAIN=local go version | cut -d ' ' -f 3 | sed 's/go*//' | tr -d '\n')
			if [[ $($SEMVER compare "v$local_goversion" v"$goversion") -ge 0 ]]; then
				goversion=$local_goversion
			else
				local_goversion=
			fi
		fi

		if [[ "$local_goversion" == "" ]]; then
			goversion=$(curl -s "https://go.dev/dl/?mode=json&include=all" | jq -r --arg regexp "^go$goversion" '.[] | select(.stable == true) | select(.version | match($regexp)) | .version' | head -n 1 | sed -e s/^go//)
		fi

		if [[ $goversion != "" ]] && [[ $($SEMVER compare "v$goversion" v1.21.0) -ge 0 ]]; then
			gotoolchain=go${goversion}
		else
			gotoolchain=go$(grep -E '^go [0-9]{1,}.[0-9]{1,}$' <"$gomod" | cut -d ' ' -f 2 | tr -d '\n').0
		fi
	fi

	echo -n "$gotoolchain"
}

function get_goversion() {
	local goversion

	goversion=$(go list -mod=readonly -f '{{ .Module.GoVersion }}')

	echo -n "go$goversion"
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

function run_bump_module() {
	local cmd
	local prefix
	local mod_tag

	cmd="$1"
	mod_tag="$(git describe --abbrev=0 --tags --match "v*")"

	if [[ "$mod_tag" =~ $SEMVER_REGEX_STR ]]; then
		local nversion
		local oversion

		oversion=${BASH_REMATCH[0]}

		nversion=v$($SEMVER bump "$cmd" "$oversion")
		git tag -a "$nversion" -m "$nversion"
	else
		error "unable to find any tag for module $prefix"
	fi
}

function run_k8s_gen() {
	rm -rf "${ROOT_DIR}/pkg/client/*"

	source "$AP_DEVCACHE_BIN/kube_codegen.sh"

	kube::codegen::gen_helpers \
		--boilerplate "${ROOT_DIR}/pkg/apis/boilerplate.go.txt" \
		"${ROOT_DIR}/pkg/apis"

	kube::codegen::gen_client \
		--output-pkg github.com/akash-network/provider/pkg/client \
		--output-dir "${ROOT_DIR}/pkg/client" \
		--boilerplate "${ROOT_DIR}/pkg/apis/boilerplate.go.txt" \
		--with-watch \
		--with-applyconfig \
		"${ROOT_DIR}/pkg/apis"
}

case "$1" in
	gotoolchain)
		get_gotoolchain
		;;
	goversion)
		get_goversion
		;;
	build-akash)
		shift
		build_akash "$@"
		;;
	build-akash-docker)
		shift
		build_akash_docker "$@"
		;;
	bump)
		shift
		run_bump_module "$@"
		;;
	k8s-gen)
		run_k8s_gen
		;;
esac
