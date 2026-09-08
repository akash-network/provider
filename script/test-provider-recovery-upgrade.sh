#!/usr/bin/env bash
set -euo pipefail

# Build existing workload state with the base code, then recover it with the
# candidate. Nothing checks out, stashes or changes the caller's working tree.
base_ref=${1:?usage: test-provider-recovery-upgrade.sh BASE_REVISION}
repo_dir=$(git rev-parse --show-toplevel)
test_dir=$(mktemp -d)
base_dir="$test_dir/base"
snapshots_dir="$test_dir/snapshots"
mkdir "$snapshots_dir"

cleanup() {
  git -C "$repo_dir" worktree remove --force "$base_dir" >/dev/null 2>&1 || true
  rm -rf "$test_dir"
}
trap cleanup EXIT

run_test() {
  local test_name=$1
  local log_file=$2
  shift 2
  if ! go test -json "$@" > "$log_file"; then
    jq -r 'select(.Action == "output") | .Output' "$log_file"
    return 1
  fi
  # Go returns success when -run matches no tests. Require the named test to
  # actually pass so a rename or build-tag mistake cannot disable this check.
  if ! jq -s -e --arg name "$test_name" \
    'any(.[]; .Action == "pass" and .Test == $name)' "$log_file" >/dev/null; then
    printf 'Required recovery test did not run: %s\n' "$test_name" >&2
    return 1
  fi
  printf 'PASS: %s\n' "$test_name"
}

git -C "$repo_dir" worktree add --detach "$base_dir" "$base_ref"

# Bootstrap the first PR introducing this test. Later bases use their own
# fixture matrix, so new candidate-only features do not become old workloads.
# Only test code is copied; all deployment/recovery production code is the base.
if [[ ! -f "$base_dir/cluster/kube/recovery_test.go" ]]; then
  cp "$repo_dir/cluster/kube/recovery_test.go" "$base_dir/cluster/kube/recovery_test.go"
  cp "$repo_dir/testdata/deployment/deployment-v2-recovery-features.yaml" "$base_dir/testdata/deployment/"
fi

(
  cd "$base_dir"
  PROVIDER_RECOVERY_EXPORT_DIR="$snapshots_dir" \
    run_test TestDeploymentRecoveryAcrossFeatures "$test_dir/base.log" \
    ./cluster/kube -run '^TestDeploymentRecoveryAcrossFeatures$' -count=1
)

cd "$repo_dir"
PROVIDER_RECOVERY_IMPORT_DIR="$snapshots_dir" \
  run_test TestDeploymentUpgradeFromSnapshots "$test_dir/candidate.log" \
  -tags=recovery_upgrade ./cluster/kube -run '^TestDeploymentUpgradeFromSnapshots$' -count=1
