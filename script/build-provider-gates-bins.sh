#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
bindir=${PROVIDER_GATES_BINDIR:-"$repo_root/.cache/provider-gates-bin"}
mkdir -p "$bindir"

# GOWORK=off so a developer's go.work cannot change what the binary links against. The
# main checkout builds readonly only; -mod=mod could rewrite go.mod/go.sum.
GOWORK=off go build -mod=readonly -o "$bindir/provider-services" ./cmd/provider-services
printf 'export AKASH_PROVIDER_BIN=%q\n' "$bindir/provider-services"

ref=${PROVIDER_UPGRADE_BASE_REF:-}
if [ -n "$ref" ] && [ "$ref" != "HEAD" ]; then
  worktree=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "git -C '$repo_root' worktree remove --force '$worktree' 2>/dev/null || true; rm -rf '$worktree'" EXIT

  # Best-effort: a base-ref build failure degrades only the upgrade gate to a same-version
  # check instead of aborting the gates that never use this binary. -mod=mod is safe to
  # fall back to only here, in a throwaway worktree.
  base=$bindir/provider-services-base
  if git -C "$repo_root" worktree add --detach "$worktree" "$ref" >/dev/null 2>&1 && (
    cd "$worktree" && export GOWORK=off &&
      { go build -mod=readonly -o "$base" ./cmd/provider-services ||
        go build -mod=mod -o "$base" ./cmd/provider-services; }
  ); then
    printf 'export AKASH_PROVIDER_BASE_BIN=%q\n' "$base"
  else
    echo "warning: could not build base ref $ref; upgrade gate will run as a same-version check" >&2
  fi
fi
