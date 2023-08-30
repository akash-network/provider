#!/usr/bin/env bash

MOD_PATH=$(go list -mod=readonly -m -f '{{ .Replace }}' "$1" 2>/dev/null | grep -v -x -F "<nil>")

[[ "${MOD_PATH}" =~ ^(\/|\.\/|\.\.\/).*$ ]] && echo -n true || echo -n false
