#!/usr/bin/env bash

MOD_PATH=$(go list -mod=readonly -m -f '{{ .Replace }}' "$1" 2>/dev/null)

if [[ "${MOD_PATH}" == "<nil>" ]]; then
	echo false
else
	[[ "${MOD_PATH}" = /* ]] && echo true || echo false
fi
