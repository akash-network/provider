#!/usr/bin/env bash
set -e

OUTDIR=$1
TAGS=$2

coverage="$OUTDIR/coverage.txt"
profile="$OUTDIR/profile.out"

set -e
echo "mode: atomic" > "$coverage"
while IFS=$'\n' read -r pkg; do
    go test -timeout 30m -race -coverprofile="$profile" -covermode=atomic -tags="$TAGS" "$pkg"
    if [ -f "$profile" ]; then
        tail -n +2 "$profile" >> "$coverage";
        rm "$profile"
    fi
done < <(go list ./... | grep -v 'mock\|pkg/client')
