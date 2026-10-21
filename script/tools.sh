#!/usr/bin/env bash

eval "$(direnv hook bash)"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
SEMVER=$SCRIPT_DIR/semver.sh

source "$SCRIPT_DIR/semver_funcs.sh"

gomod="$SCRIPT_DIR/../go.mod"

function get_gotoolchain() {
	local gotoolchain
	local goversion

	gotoolchain=$(grep -E '^toolchain go[0-9]{1,}.[0-9]{1,}.[0-9]{1,}$' <"$gomod" | cut -d ' ' -f 2 | tr -d '\n')
	goversion=$(grep -E '^go [0-9]{1,}.[0-9]{1,}(.[0-9]{1,})?$' <"$gomod" | cut -d ' ' -f 2 | tr -d '\n')

	if [[ ${gotoolchain} == "" ]]; then
		# determine go toolchain from go version in go.mod. A full version is used
		# as is, never swapped for a newer local go: early patches of a newer minor
		# can lack fixes backported to this one (go1.27.0-go1.27.2 lack go1.26.10's
		# CVE-2026-111943 fix), and the result also selects the goreleaser-cross tag.
		# A toolchain line cannot pin this instead: go mod tidy drops one that
		# matches the go line.
		if [[ ! $goversion =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
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

# require_src <command> <dir>
# Exits unless <dir> is set: depending on the bash version, cd "" either fails
# with an unclear message or succeeds and stays in the current directory, where
# the build would then run.
function require_src() {
	if [[ -z "$2" ]]; then
		echo "$1: source directory required" >&2
		exit 1
	fi
}

function build_akash() {
	local tmp_akash
	tmp_akash="${AP_DEVCACHE_BIN}/akash"

	require_src build-akash "$1"
	cd "$1" || exit 1

	direnv allow
	_direnv_hook

	cp "$AKASH" "$tmp_akash"

	make akash
}

function build_akash_docker() {
	require_src build-akash-docker "$1"
	cd "$1" || exit 1
	direnv allow
	_direnv_hook
	make docker-image
}

# build_akt <akt-src> <dest>
# Builds akt from a local checkout under the checkout's own direnv environment
# (as build_akash does for node), then moves it onto <dest>. go build refuses to
# overwrite a file it does not recognise (e.g. the universal release binary),
# so the build goes to a temporary path next to <dest>.
function build_akt() (
	local src=$1
	local dest
	local partial

	require_src build-akt "$src"
	mkdir -p "$(dirname "$2")" || exit 1
	dest="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")" || exit 1
	partial="${dest}.partial.$$"
	trap 'rm -f "$partial"' EXIT

	cd "$src" || exit 1
	direnv allow
	_direnv_hook

	GOWORK=off make akt AKT="$partial" || exit 1
	mv -f "$partial" "$dest" || exit 1
)

# build_akt_docker <akt-src> <outdir>
# Cross-compiles akt from a local checkout with its own goreleaser snapshot
# (make release-snapshot) and copies the linux binaries to
# <outdir>/linux_<arch>/akt. Artifact paths in artifacts.json may be absolute
# container paths, so they are re-rooted at the checkout's dist directory.
function build_akt_docker() (
	local src=$1
	local outdir
	local arch
	local path
	local dest

	require_src build-akt-docker "$src"
	mkdir -p "$2" || exit 1
	outdir=$(cd "$2" && pwd) || exit 1

	# akt's snapshot runs goreleaser-cross pinned by digest with
	# --platform linux/amd64 by default. Docker's classic image store keeps one
	# platform per digest, so on arm64 hosts that collides with the native image
	# the provider build pulls for the same tag ("cannot overwrite digest").
	# Run akt's snapshot on the host platform as well (also avoids emulation).
	local platform=linux/amd64
	case "$(uname -m)" in
	arm64 | aarch64) platform=linux/arm64 ;;
	esac

	cd "$src" || exit 1
	direnv allow
	_direnv_hook

	# GOWORK=off as in build_akt: a local go.work may point at sibling checkouts
	# that are not mounted into akt's goreleaser container.
	GOWORK=off make release-snapshot GORELEASER_PLATFORM="$platform" || exit 1

	for arch in amd64 arm64; do
		path=$(jq -r --arg arch "$arch" \
			'[.[] | select(.type == "Binary" and .goos == "linux" and .goarch == $arch)][0].path // empty' \
			.cache/goreleaser/artifacts.json)
		path=".cache/goreleaser/${path#*.cache/goreleaser/}"
		if [[ ! -f "$path" ]]; then
			echo "akt: no linux/$arch binary in the akt snapshot ($path)" >&2
			exit 1
		fi

		dest="$outdir/linux_$arch/akt"
		if ! mkdir -p "$(dirname "$dest")" ||
			! cp "$path" "$dest.partial.$$" ||
			! chmod 0755 "$dest.partial.$$" ||
			! mv -f "$dest.partial.$$" "$dest"; then
			rm -f "$dest.partial.$$"
			echo "akt: cannot stage $dest" >&2
			exit 1
		fi
	done
)

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
	build-akt)
		shift
		build_akt "$@"
		;;
	build-akt-docker)
		shift
		build_akt_docker "$@"
		;;
	bump)
		shift
		run_bump_module "$@"
		;;
	k8s-gen)
		run_k8s_gen
		;;
esac
