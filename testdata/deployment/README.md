Every YAML fixture in this directory runs through
`TestDeploymentRecoveryAcrossFeatures`. Add a valid SDL here when adding a
manifest feature; new files and all their deployment groups are discovered
automatically. Invalid-input fixtures belong elsewhere.

The matrix combines these manifests with ordinary, NVIDIA, AMD, InfiniBand,
RoCE, SNP and TDX scheduler settings, including confidential GPU workloads and
disabled attestation. Add a matrix case when introducing a new scheduler or
provider configuration. Cases share one recovery contract rather than
maintaining feature-specific lists of fields to compare.

The contract exercises Deploy, persisted Manifest recovery, three fresh-client
recoveries, an unchanged resend, a real tenant update, and three more recoveries.
It checks the complete pod template and that an update affects only the intended
service. API serialization prevents internal resource-quantity caches from
producing false alarms. GPU, RDMA and TEE cases use fake clients to exercise the
provider path without requiring special hardware.

CI also runs `script/test-provider-recovery-upgrade.sh BASE_REVISION`. The base
revision creates workload snapshots in an isolated temporary worktree. The
candidate recovers those exact objects, retaining operator overrides while
using its own defaults. This detects new defaults that would replace existing
pods even when same-version Create and Update agree. Missing snapshots fail the
check. Snapshots are temporary artifacts, not expected-output files to refresh
when the candidate changes.

`TestDeploymentRecoveryKeepsPodsRunning` runs in the existing Kubernetes
integration job and checks actual API-server versions, controller convergence,
pod UIDs and container restarts for both Deployments and StatefulSets. It covers
legacy labels, external metadata edits, unchanged resends and tenant updates.

Local commands:

```sh
go test ./cluster/kube -run '^TestDeploymentRecoveryAcrossFeatures$' -count=1
bash script/test-provider-recovery-upgrade.sh origin/main
go test -tags=k8s_integration ./cluster/kube -run '^TestDeploymentRecoveryKeepsPodsRunning$' -count=1
```

These checks cover generated workload state and the Kubernetes recovery path.
Feature-specific functional tests, including hardware tests where needed, are
still required. A new opt-in feature needs a fixture that enables it.
