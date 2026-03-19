# Provider Config Proposal

## Requirements

- Provider should be configured by one config.
- Single source of truth that can be watched by provider; changes in the source must be propagated to all watching providers.
- Hot reload - provider should apply config changes on the fly.
- Config must not be public.
- Follow the inventory operator config style.

## Questions to clarify

- Do we want to share a single config between providers located in different K8s clusters? (If yes, ConfigMap is not suitable.)

## Terminology

- **Ops** (human operator): Person who runs and maintains the provider. Receives notifications, decides when to restart.
- **K8s operator**: Controller running in Kubernetes (e.g. inventory operator) that manages provider deployments and related resources.
- **Startup config**: Values (e.g. cluster.k8s, manifest_namespace) that require a restart to take effect.
- **Runtime config**: Values that can be reloaded on the fly without restart.


## Proposed solution

### Config format

YAML format with subsections per module (like inventory operator).

<details>
<summary>Expand to see config example</summary>

```yaml
version: v1
cluster:
  k8s: true
  manifest_namespace: lease
  public_hostname: ""
  node_port_quantity: 1
  wait_ready_duration: 5s
  overcommit:
    cpu: 0
    memory: 0
    storage: 0
  deployment:
    ingress_static_hosts: false
    ingress_domain: ""
    ingress_expose_lb_hosts: false
    network_policies_enabled: true
    runtime_class: gvisor
    blocked_hostnames: []
    docker_image_pull_secrets: ""

bidengine:
  pricing_strategy: scale
  deposit: "5000000uakt"
  timeout: 5m
  scale:
    cpu: "0"
    memory: "0"
    storage: "0"
    endpoint: "0"
    ip: "0"

gateway:
  listen_address: "0.0.0.0:8443"
  grpc_listen_address: "0.0.0.0:8444"
  tls:
    cert: ""
    key: ""

monitor:
  max_retries: 40
  retry_period: 4s
  retry_period_jitter: 15s
  healthcheck_period: 10s
  healthcheck_period_jitter: 5s

balance_checker:
  withdrawal_period: 24h
  lease_funds_check_interval: 10m

cert_issuer:
  enabled: false

# ... other sections
```

</details>

### Hot reload

Decisions:

1. **Auto-restart on config change?** No - to avoid unexpected downtime. Notify Ops; they restart when ready.

2. **Mixed change (runtime + startup):** Apply runtime values only. Ops must be notified that restart is required.

3. **Module re-init without full process restart?** Possibly yes, in a later iteration. Cluster and bidengine have shared state, so a clean restart is recommended for those modules. Some values (e.g. listen address) could be applied without restart by redesign - start new server, close old one.

**Restart notification** (when startup config changes, notify Ops; they restart when ready). Fully automated: Ops is pushed the notification, no manual checks.

Flow:
1. Provider loads config from source (ConfigMap, S3, etc.) and watches or polls for changes.
2. Provider detects startup config change. Applies runtime changes only.
3. Provider emits notification (Prometheus metric, K8s Event, or pub/sub message).
4. Ops receives notification automatically (Prometheus alert, Slack, PagerDuty, etc.) - no active check required.
5. Ops restarts provider when ready (maintenance window, low traffic, etc.).

| Config source | Notification channel |
|---------------|----------------------|
| **ConfigMap** | Provider creates K8s Event (alert on Event) or sets Prometheus metric; Ops gets paged. |
| **S3** | Provider sets Prometheus metric `provider_config_restart_required=1`; Ops alert fires. |
| **HTTP** | Same as S3. |
| **Redis** | Provider publishes to `restart_required` channel; consumer triggers alert (push to Ops). |
| **Consul** | Provider sets Prometheus metric; or consumer watches KV and triggers alert. |

Provider keeps running normally. Notification (metric, Event, pub/sub) is a passive marker - no change to provider behavior, no traffic drain. Ops gets alerted and restarts when ready.

## Solution comparison

### By scenario

| Scenario | Best fit |
|----------|----------|
| **Single cluster** | ConfigMap + K8s watch |
| **Multi-cluster, minimal infra** | S3 + poll |
| **Multi-cluster, near-instant updates** | Redis, Consul or Vault |


### S3 vs Other Config Sources

| Criteria | S3 + poll | ConfigMap + K8s watch | Redis | Consul | HTTP + poll | Vault |
|----------|-----------|------------------------|-------|--------|-------------|-------|
| **Single source of truth** | Yes | Per cluster | Yes | Yes | Yes | Yes |
| **Multi-cluster** | Yes | No | Yes* | Yes | Yes | Yes |
| **Watch / push** | No (poll) | Yes | Yes (pub/sub) | Yes | No (poll) | Yes (KV watch) |
| **Auth** | Access key or IAM | K8s SA + RBAC | Password | ACL token | Bearer, mTLS, OAuth2 | AppRole, K8s auth |
| **Auth: set once** | Yes (key) | Yes (SA) | Yes (password) | Yes (token) | Depends | Yes (AppRole) |
| **Auth: outside cloud** | Access key | N/A (in-cluster) | Password | Token | Bearer, mTLS | AppRole |
| **Infra to run** | None (managed) | None | Redis | Consul | HTTP server | Vault |
| **Provider deps** | AWS SDK | K8s client (existing) | redis client | consul client | net/http | vault client |
| **Complexity** | Low | Low | Medium | Medium | Low-Medium | High |
| **Max config delay** | Poll interval (e.g. 30s) | Seconds | Seconds | Seconds | Poll interval | Seconds |

\* Redis must be reachable from all clusters (shared instance or replication).



### Trade-offs

| Solution | Pros | Cons |
|----------|------|------|
| **S3** | No extra infra, managed, multi-cloud, simple auth | Polling only, config delay up to poll interval |
| **ConfigMap** | Native K8s, real-time watch, no secrets | Single cluster only |
| **Redis** | Pub/sub, fast updates, simple auth | Run and operate Redis |
| **Consul** | KV + watch, multi-datacenter, ACL | Run and operate Consul |
| **HTTP** | Flexible, any backend | Need server + watch/poll strategy |
| **Vault** | Strong auth, KV watch | Heavy, more setup |

## Migration plan

1. **Phase 1 - Struct + loader**: Define Go structs for config, implement YAML loader. Keep flags; map flags to struct fields during transition.
2. **Phase 2 - Remote source**: Add S3/ConfigMap backend as primary config source. Flags override remote values (backward compat).
3. **Phase 3 - Remove flags**: Deprecate individual flags; remote config becomes the only input. Env vars for secrets only (e.g. `AKASH_PROVIDER_KEY`).
4. **Phase 4(optional) - File fallback**: Add optional local file for dev; used when remote is not configured or unreachable.

## Go implementation

- **YAML parsing**: `gopkg.in/yaml.v3` (already in go.mod)
- **Config struct + merge**: Custom structs with `mapstructure` tags; `github.com/go-viper/mapstructure/v2` for YAML-to-struct
- **File watch**: `fsnotify` or `github.com/fsnotify/fsnotify` for local file; K8s watch for ConfigMap; S3 poll
- **No Viper for new config**: Current code uses `spf13/viper` with flags. New design: explicit load (YAML unmarshal + optional merge), no Viper. Simplifies precedence and avoids flag/config coupling.

## Local override of global config

Use case: global config (S3/ConfigMap) shared by providers; one provider needs different values (e.g. dev, debugging, cluster-specific).

| Solution | How it works | Pros | Cons |
|----------|--------------|------|------|
| **Override file** | Load global first, then `config.local.yaml` (or path from `--config-override`). Deep-merge; local wins. | Simple, explicit, no extra infra | Two files to manage; override path must be passed |
| **Env per field** | `CLUSTER_DEPLOYMENT_INGRESS_DOMAIN=dev.example.com` overrides `cluster.deployment.ingress_domain`. | No extra files, 12-factor | Verbose for nested keys; env proliferation |
