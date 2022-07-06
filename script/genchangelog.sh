#!/usr/bin/env bash

PATH=$PATH:$(pwd)/.cache/bin
export PATH=$PATH

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"

if [[ $# -ne 2 ]]; then
	echo "illegal number of parameters"
	exit 1
fi

to_tag=$1

version_rel="^[v|V]?(0|[1-9][0-9]*)\\.(\\d*[0-9])\\.(0|[1-9][0-9]*)$"
version_prerel="^[v|V]?(0|[1-9][0-9]*)\\.(\\d*[0-9])\\.(0|[1-9][0-9]*)(\\-[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?(\\+[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$"

if [[ -z $("${SCRIPT_DIR}"/semver.sh get prerel "$to_tag") ]]; then
	tag_regexp=$version_rel
else
	tag_regexp=$version_prerel
fi

query_string="$to_tag"
git-chglog --config .chglog/config.yaml --tag-filter-pattern="$tag_regexp" --output "$2" "$query_string"
