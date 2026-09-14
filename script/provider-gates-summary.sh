#!/usr/bin/env bash
# Deliberately no `set -e`: a missing or truncated log must still render the
# "did not complete" table rather than abort with no output.
set -uo pipefail

# Render a workload-health table from a `go test -v` log of the provider disruption
# gates. Each gate deploys a real tenant workload, performs a provider restart or
# cross-version upgrade, and verifies the pods are not rolled, restarted, or deleted;
# a passing gate means the workload stayed healthy.
#
# Usage: provider-gates-summary.sh <log-file> [gate-step-outcome]
# gate-step-outcome is the GitHub step conclusion (success|failure|cancelled|...); it
# catches the case where the run died before producing any go-test output at all.

log_file=${1:-/dev/stdin}
gate_outcome=${2:-}
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

# The run did not finish cleanly unless there is positive evidence it did. go test
# prints a "FAIL <pkg>" line even when it panics, times out, or is killed, so that line
# is never treated as proof; instead default to inconclusive whenever a gate started
# without finishing, the step failed without a clean per-gate FAIL, or nothing ran at
# all and the step did not clearly pass.
run_incomplete=false
if grep -qE '\[build failed\]|^panic:' <<<"$log"; then
  run_incomplete=true
fi
for gate in "${gates[@]}"; do
  name=${gate%%|*}
  if grep -qE "^=== RUN   ${name}\$" <<<"$log" && [ -z "$(result_of "$name")" ]; then
    run_incomplete=true
  fi
done
if [ "$gate_outcome" = "failure" ] && ! grep -qE '^--- FAIL: TestProvider' <<<"$log"; then
  run_incomplete=true
fi
if ! grep -qE '^--- (PASS|FAIL|SKIP): TestProvider' <<<"$log" && [ "$gate_outcome" != "success" ]; then
  run_incomplete=true
fi

icon_of() {
  case "$(result_of "$1")" in
  FAIL) echo x ;;              # workload was disrupted
  PASS) echo white_check_mark ;; # workload stayed healthy
  SKIP) echo fast_forward ;;  # skipped on purpose
  *) "$run_incomplete" && echo warning || echo heavy_minus_sign ;;
  esac
}

printf '## Workload health across provider lifecycle events\n\n'
printf 'Each check deploys a real tenant workload, performs the event, then verifies the '
printf 'pods are not rolled, restarted, or deleted.\n\n'
if "$run_incomplete"; then
  printf '> :warning: **Some checks did not complete** (build error, panic, timeout, or '
  printf 'the run was killed). Inconclusive results are marked below; see the job log.\n\n'
fi
printf '| Verification | Workload healthy |\n'
printf '| --- | :---: |\n'
for gate in "${gates[@]}"; do
  printf '| %s | :%s: |\n' "${gate#*|}" "$(icon_of "${gate%%|*}")"
done
printf '\n:white_check_mark: healthy &middot; :x: disrupted &middot; '
printf ':warning: inconclusive &middot; :fast_forward: skipped &middot; '
printf ':heavy_minus_sign: not run\n'
