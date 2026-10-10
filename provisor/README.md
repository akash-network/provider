# provisor

`provisor/verify` runs a signed release-manifest supply chain: a key-set, a channel pointer, and a manifest, each fetched over HTTP and checked against a threshold of Ed25519 signatures before its bytes are trusted.
`devkit` is a local stand-in for the real release pipeline.
It signs the same three documents the pipeline will eventually produce and serves them over HTTP, so the verification chain can be exercised end to end without any real infrastructure.
`provisor-fetch` runs that chain once against a running server and reports the outcome.

## Compiled-in trust roots

The compiled-in trust roots are embedded into the binary at compile time, via `go:embed` on `trust/roots/*.json`.
Regenerating `trust/roots/dev.json` has no effect on an already-built `devkit` or `provisor-fetch` binary.
Both must be rebuilt for a new dev root to take effect.

## Dev loop

The dev loop (generating dev keys, running `devkit serve`, running `provisor-fetch` against it) runs inside the kind cluster managed by `_run/kube`, not as bare binaries on the host.
See `_run/kube/README.md` for how to bring it up, why key generation must happen before the image is built, and how to read the fetch result.

## Fault injection

Every flag to `devkit serve` breaks exactly one step of the chain, so each can be demonstrated on its own: pass the flag to `serve`, then run `provisor-fetch` against it and read the refusal reason it prints.

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

## Commands

`devkit keys` and `devkit serve` must run from inside `provisor/`, since both read and write paths relative to the current directory (`.devkeys/`, `trust/roots/dev.json`).
`provisor-fetch` requires `--keyset-url`, `--channel-url`, and `--installed-release`; `--last-keyset-version` defaults to `0` and `--allowed-registry` (repeatable) defaults to `ghcr.io/akash-network`.
It contains nothing that can skip or weaken verification: no flag, no environment variable, no build tag bypasses any check.
It exits `0` on acceptance and non-zero on any refusal, so it is usable in a script.
