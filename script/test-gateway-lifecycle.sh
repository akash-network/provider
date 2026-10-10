#!/usr/bin/env bash
# Full local-chain/provider/gateway validation. Requires a prepared disposable cluster.
# The suite checks kube-system's akash.network/local-validation=true label before startup.
set -euo pipefail
: "${AKASH_E2E_KUBECONFIG:?Set the kubeconfig for the disposable validation cluster}"
: "${KUBE_INGRESS_IP:?Set the reachable gateway IP}"
: "${KUBE_INGRESS_PORT:?Set the reachable gateway port}"
export AKASH_E2E_ARTIFACT_DIR="${AKASH_E2E_ARTIFACT_DIR:-$(mktemp -d /tmp/akash-gateway-e2e.XXXXXX)}"
mkdir -p "$AKASH_E2E_ARTIFACT_DIR"
export TEST_INTEGRATION=true
validation_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$validation_root"
kubectl --kubeconfig "$AKASH_E2E_KUBECONFIG" version -o json > "$AKASH_E2E_ARTIFACT_DIR/kubernetes-version.json"
kubectl --kubeconfig "$AKASH_E2E_KUBECONFIG" get pods -A -o json > "$AKASH_E2E_ARTIFACT_DIR/pods-before.json"
go test -count=1 -tags=e2e -run '^TestGatewayAPISuite$/^TestLocalGatewayLifecycle$' -v -timeout=20m ./integration 2>&1 | tee "$AKASH_E2E_ARTIFACT_DIR/lifecycle.log"
