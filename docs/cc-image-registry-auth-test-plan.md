# Test Plan — Private-image pulls for Confidential Compute (no KBS/attestation)

## What this validates

For confidential-compute (Kata/CoCo) workloads the container image is pulled
*inside* the guest by image-rs, so the host-side `imagePullSecrets` the provider
sets never reach it and **private images fail to pull**. This change delivers the
tenant's registry credentials into the guest via **measured Kata init-data**, so
image-rs can pull private images **without a KBS and without attestation** —
uniformly for SNP, TDX and GPU workloads.

Two coordinated changes are under test:

- **kata-agent** (`feature/initdata-image-registry-auth`): materializes an
  `auth.json` entry from init-data to `/run/confidential-containers/initdata/auth.json`
  (0600), in addition to the existing `aa.toml`/`cdh.toml`.
- **provider** (`feature/cc-image-registry-auth`): for CC workloads with SDL
  credentials, emits the `io.katacontainers.config.hypervisor.cc_init_data`
  annotation carrying `cdh.toml` (pointing image-rs at the local `auth.json` via
  `authenticated_registry_credentials_uri = file:///run/confidential-containers/initdata/auth.json`)
  plus the `auth.json` itself. The host-side `imagePullSecret` is still set for
  the host-side manifest resolve.

## Design checkpoint to confirm first (highest risk)

The provider points image-rs at the credentials via **cdh.toml
`authenticated_registry_credentials_uri`** (delivered through init-data). Phase-0
validated the *kernel-param* knob (`agent.image_registry_auth`) with `kbs://`;
the cdh.toml + `file://` combination is what we ship here. **TC1 is the gate**: if
image-rs does not consume `authenticated_registry_credentials_uri` from the
init-data cdh.toml, fall back to also emitting
`agent.image_registry_auth=file:///run/confidential-containers/initdata/auth.json`
via `io.katacontainers.config.hypervisor.kernel_params` — but first confirm Kata
**appends** annotation kernel-params to the base set (so GPU params such as
`nvrc.smi.srs=1` survive); if it replaces, do not use kernel_params for GPU.

## Prerequisites

1. **Patched guest images.** Build the kata guest rootfs from
   `feature/initdata-image-registry-auth` for both confidential variants:
   - `kata-ubuntu-noble-confidential.image` (CPU CC — used by SNP and TDX)
   - `kata-ubuntu-noble-nvidia-gpu-confidential.image` (GPU CC)

   Build via the kata packaging tooling (`tools/packaging/kata-deploy/local-build`
   / osbuilder) with dm-verity, then package into a **custom kata-deploy image**
   and roll it to the nodes. Record the new guest measurement (the launch
   measurement changes with any rootfs rebuild; init-data content does not).

2. **Provider build** from `feature/cc-image-registry-auth` (`make dev-push` per
   repo conventions), deployed to the running provider.

3. **A private test image** the provider account can pull, e.g.
   `ghcr.io/<org>/nginx-private:test`, plus a read-only pull token supplied
   through the SDL `credentials` block.

4. **A canary node** with free CC GPUs (node3 in the current cluster: 8×
   `nvidia.com/pgpu`, verify `0` in use before the GPU case).

## Test cases

### TC1 — CPU-CC private pull (gate)
- Deploy a CC (CPU) lease (`tee_type: cpu`) with a **private** image and SDL
  `credentials`, landing on `kata-qemu-snp`.
- **Pass:** pod reaches `Running`; no `[CDH] ... Not authorized` /
  `Get resource failed`; the tenant image is pulled in-guest.
- **Verify:**
  - `kubectl get pod <p> -o jsonpath='{.metadata.annotations.io\.katacontainers\.config\.hypervisor\.cc_init_data}'` is set.
  - `nydus-for-kata-tee` snapshotter shows a guest-pull for the image (guest-pull
    active, not a host overlayfs pull).
- If this fails at the `[CDH]` stage, apply the kernel_params fallback (see
  design checkpoint) and retry before proceeding.

### TC2 — GPU-CC private pull (the primary objective)
- Same as TC1 but `tee_type: cpu-gpu`, `kata-qemu-nvidia-gpu-snp`, 1
  `nvidia.com/pgpu`, on a node with a free GPU.
- **Pass:** pod `Running` with the private image; **no attestation involved**, so
  the RTX Pro 6000 / Blackwell GPU-verifier gap is not exercised.
- This is the case that previously failed under the KBS/attestation approach.

### TC3 — TDX (deferred / other cluster)
- Not runnable on the current all-SNP cluster. On TDX hardware, repeat TC1/TC2
  with `kata-qemu-tdx` / `kata-qemu-nvidia-gpu-tdx`. The path is
  platform-agnostic (no platform attestation), so behavior should match SNP.

### TC4 — Tenant isolation (multi-tenant safety)
- Deploy **two** CC leases from different owners, each with a **different**
  private repo on the **same registry host** (e.g. `ghcr.io/tenantA/*` and
  `ghcr.io/tenantB/*`), each with only its own credentials.
- **Pass:** each pulls only its own image. Tenant B **cannot** pull tenant A's
  image (B's guest only ever receives B's `auth.json`; the provider builds each
  guest's init-data, tenants cannot influence it).
- **Negative:** a lease referencing tenant A's private repo **without**
  credentials must fail to pull (no cross-tenant credential reuse — unlike the
  nydus keychain).

### TC5 — No regressions
- **Public image on CC** (no `credentials`): still pulls (no `cc_init_data`
  annotation emitted; verify annotation absent).
- **Private image, non-CC** (plain `runc`/`kata-qemu`): still pulls via host-side
  `imagePullSecret`; verify no `cc_init_data` annotation.
- **Existing CC deployments** unaffected: init-data without an `auth.json` key
  behaves exactly as before (covered by kata-agent unit tests
  `test_materialize_skips_absent_keys`).

### TC6 — Measurement stability
- Launch two CC leases with **different** credentials.
- **Pass:** the **guest launch measurement is identical** across both (credentials
  live only in the init-data field, not the launch measurement). The `cc_init_data`
  digest differs per lease (expected — it is config/identity, not the launch
  image). Capture from the attestation sidecar / quote if available.

### TC7 — Negative: bad/absent credentials
- CC lease with **wrong** credentials → pull fails cleanly with an auth error
  (confirms the guest genuinely uses the delivered creds, not a cache).
- CC lease with `credentials` omitted for a private image → fails to pull (no
  annotation emitted; nothing to deliver).

### TC8 — Lifecycle
- Close a CC lease → the pod and its `cc_init_data` annotation are removed with
  the pod; no credential material persists on the node (init-data lives only in
  the ephemeral guest). Confirm no leftover secrets after teardown.

## How to observe (debug console is disabled on these nodes)

- **Provider side:** inspect the emitted annotation on the pod (jsonpath above);
  base64-decode + gunzip it to confirm it carries `cdh.toml` + `auth.json`.
- **Guest-pull confirmation:** `journalctl -u containerd` on the node — a guest
  pull goes through the `nydus-for-kata-tee` snapshotter; a host overlayfs
  "pull and unpack" means guest-pull did not trigger.
- **Success signal:** container `Running`, no `[CDH]` error in pod events.
- **Failure triage:** `[CDH] ... Not authorized` → guest received no/invalid
  creds; `[CDH] Get resource failed` → CDH could not resolve the auth URI
  (check cdh.toml delivery / the kernel_params fallback); host-side
  `401 Unauthorized` → the host `imagePullSecret` is missing (manifest resolve).

## Rollback

- Provider: redeploy the previous provider build (the annotation simply stops
  being emitted; CC private pulls revert to failing, non-CC unaffected).
- Guest image: roll back the kata-deploy image to the stock guest images. The
  extra `auth.json` init-data key is simply ignored by the stock agent, so a
  provider emitting it against a stock guest is a no-op for that file (pull
  fails as before) — safe, no crash.

## Exit criteria

- [ ] TC1 and TC2 pass (private pull on CPU-CC and GPU-CC, no attestation).
- [ ] TC4 confirms per-tenant isolation (no cross-tenant credential use).
- [ ] TC5 shows no regression for public/non-CC/existing workloads.
- [ ] TC6 confirms the launch measurement is unchanged by credentials.
- [ ] TC7/TC8 confirm clean failure and no credential persistence.
- [ ] The working knob (cdh.toml vs kernel_params) is recorded for rollout.
