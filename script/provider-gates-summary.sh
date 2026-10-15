#!/usr/bin/env bash
# Deliberately no `set -e`: a missing or truncated log must still render the
# "did not complete" table rather than abort with no output.
set -uo pipefail

# Render a workload-health table from a `go test -v` log of the provider disruption
# gates. Each gate deploys a real tenant workload, performs a provider restart or
# cross-version upgrade, and verifies the pods are not rolled, restarted, or deleted;
# a passing gate means the workload stayed healthy.
#
# Usage: provider-gates-summary.sh <log-file>

log_file=${1:-/dev/stdin}
log=$(cat "$log_file" 2>/dev/null || true)

# runner test name | human-readable verification
gates=(
  "TestProviderSubprocessSmoke|Workload deploys and runs (subprocess provider)"
  "TestProviderRestart|Stays healthy across a same-version provider restart"
  "TestProviderRestartTEESNP|Stays healthy across a restart (SNP confidential compute)"
  "TestProviderUpgrade|Stays healthy across a cross-version provider upgrade"
  "TestProviderUpgradeTEESNP|Stays healthy across an upgrade (SNP confidential compute)"
)

# The result of a gate is a `--- PASS/FAIL/SKIP: <name>` line, anchored with a trailing
# space so TestProviderRestart does not match the TestProviderRestartTEESNP line.
result_of() { grep -aoE "^--- (PASS|FAIL|SKIP): $1 " <<<"$log" | grep -oE 'PASS|FAIL|SKIP' | head -1; }

# Every listed gate is expected to run. The summary is inconclusive if any of them
# produced no result line: a build error, panic, timeout, cancellation, or a renamed or
# deselected gate all look like a missing result. A clean run has a result for every
# gate; a genuine per-gate failure has a FAIL result and is NOT inconclusive.
run_incomplete=false
for gate in "${gates[@]}"; do
  if [ -z "$(result_of "${gate%%|*}")" ]; then
    run_incomplete=true
    break
  fi
done

icon_of() {
  case "$(result_of "$1")" in
  FAIL) echo x ;;              # workload was disrupted
  PASS) echo white_check_mark ;; # workload stayed healthy
  SKIP) echo fast_forward ;;  # skipped on purpose
  *) echo warning ;;          # no result: the run did not complete this gate
  esac
}

printf '## Workload health across provider lifecycle events\n\n'
printf 'Each check deploys a real tenant workload, performs the event, then verifies the '
printf 'pods are not rolled, restarted, or deleted.\n\n'
if "$run_incomplete"; then
  printf '> :warning: **Some checks did not complete** (build error, panic, timeout, '
  printf 'cancellation, or a missing gate). Inconclusive results are marked below; see '
  printf 'the job log.\n\n'
fi
printf '| Verification | Workload healthy |\n'
printf '| --- | :---: |\n'
for gate in "${gates[@]}"; do
  printf '| %s | :%s: |\n' "${gate#*|}" "$(icon_of "${gate%%|*}")"
done
printf '\n:white_check_mark: healthy &middot; :x: disrupted &middot; '
printf ':warning: inconclusive &middot; :fast_forward: skipped\n'
