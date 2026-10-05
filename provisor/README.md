# provisor

`provisor/verify` runs a signed release-manifest supply chain: a key-set, a channel pointer, and a manifest, each fetched over HTTP and checked against a threshold of Ed25519 signatures before its bytes are trusted.
`devkit` is a local stand-in for the real release pipeline.
It signs the same three documents the pipeline will eventually produce and serves them over HTTP, so the verification chain can be exercised end to end without any real infrastructure.
`provisor-fetch` runs that chain once against a running server and reports the outcome.

## Dev loop

The compiled-in trust roots are embedded into the binary at compile time, via `go:embed` on `trust/roots/*.json`.
Regenerating `trust/roots/dev.json` has no effect on an already-built `devkit` or `provisor-fetch` binary.
Both must be rebuilt for a new dev root to take effect.
The `serve` and `fetch` Makefile targets rebuild before they run, for exactly this reason.
`devkeys` does not, since generating keys writes no binary.

1. `make devkeys` generates two root keypairs and two operational keypairs under `.devkeys/`, and writes the root public keys to `trust/roots/dev.json`.
   Re-running this refuses to overwrite existing keys.
   Pass `make devkeys ARGS=--force` to regenerate anyway.
2. `make serve` rebuilds `devkit` and starts it, serving a signed key-set, channel pointer, and manifest at `/keyset`, `/channel`, and `/manifest` on `127.0.0.1:8099`.
3. In another terminal, `make fetch` rebuilds `provisor-fetch` and runs it against the running server, printing the accepted release or the single refusal reason.

`devkit serve`'s defaults describe a plausible release `0.18.0`, superseding `0.17.5`, upgradable from `0.16.0` onward.
`make fetch` passes `--installed-release=0.17.5`, which accepts that release as a clean upgrade.

## Fault injection

Every flag to `devkit serve` breaks exactly one step of the chain, so each can be demonstrated on its own: pass the flag to `serve`, then run `fetch` against it and read the refusal reason it prints.

| `devkit serve` flag | Refusal reason |
| --- | --- |
| `--expired` | `Expired` |
| `--release=0.17.5` (at or below `--installed-release`) | `NotMonotonic` |
| `--min-upgradable-from=0.18.0` (above `--installed-release`) | `BelowUpgradeFloor` |
| `--schema-version=2` | `UnsupportedSchema` |
| `--unpinned` | `UnpinnedReference` |
| `--registry=ghcr.io/evil-example` | `DisallowedRegistry` |
| `--manifest-signatures=1` | `SignatureThresholdNotMet` |
| `--corrupt-digest` | `DigestMismatch` |
| `--keyset-expired` | `KeySetExpired` |
| `--keyset-version=1` (at or below `--last-keyset-version`) | `KeySetReplayed` |
| `--unknown-signer` | `UnknownSigningKey` |

Pass the flag through `make serve ARGS=--expired`, or build once with `make serve` running in the background and invoke `./bin/devkit serve <flag>` directly.

## Commands

`devkit keys` and `devkit serve` must run from inside `provisor/`, since both read and write paths relative to the current directory (`.devkeys/`, `trust/roots/dev.json`).
`provisor-fetch` requires `--keyset-url`, `--channel-url`, and `--installed-release`; `--last-keyset-version` defaults to `0` and `--allowed-registry` (repeatable) defaults to `ghcr.io/akash-network`.
It contains nothing that can skip or weaken verification: no flag, no environment variable, no build tag bypasses any check.
It exits `0` on acceptance and non-zero on any refusal, so it is usable in a script.
