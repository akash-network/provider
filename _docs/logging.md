# Logging

Every `provider-services` command (`run`, `operator inventory|ip|hostname`, `migrate run`, ...) and the
attestation sidecar use the same logger (`tools/logging`, backed by zap). All logs are written to **stdout**.

## Options

Each option can be set as a flag, an environment variable (`AP_` or `AKASH_` prefix; `AP_` wins when both
are set) or a config file key. Operator subcommands (`operator inventory|ip|hostname`) do not read the config
file: set their options with flags or environment variables.

| Flag              | Environment        | Values                                                        | Default       |
|-------------------|--------------------|---------------------------------------------------------------|---------------|
| `--log_level`     | `AP_LOG_LEVEL`     | `trace\|debug\|info\|warn\|error\|fatal\|panic` or a module filter | `info`        |
| `--log_format`    | `AP_LOG_FORMAT`    | `plain\|json`                                                  | `plain`       |
| `--log_color`     | `AP_LOG_COLOR`     | `true\|false`                                                  | `true`        |
| `--log_timestamp` | `AP_LOG_TIMESTAMP` | `off\|rfc3339\|rfc3339nano\|kitchen` (empty value = `off`)     | `rfc3339nano` |
| `--trace`         | `AP_TRACE`         | `true\|false` — stack traces on error entries                 | `false`       |

- `log_format=json` always disables colors, whatever `log_color` says.
- `trace` and `debug` are the same level.
- With `trace=false` an error is logged as its message only; with `trace=true` error entries also carry
  `stacktrace` and, for errors that support it, `errVerbose`.

In Kubernetes, use JSON output:

```yaml
- AP_LOG_FORMAT=json
```

### Module filter

`log_level` accepts a comma separated list of `module:level` pairs. `*` sets the level for entries
whose module is not listed. Without a `*` entry, unlisted modules are not filtered.

```shell
provider-services run --log_level 'bidengine-order:debug,deployment-manager:debug,*:info'
provider-services operator inventory --log_level 'operator-inventory.nodes:debug,*:warn'
```

Filtering happens before an entry is encoded, so suppressed entries cost almost nothing.

## Output

`plain`:

```text
2026-10-12T12:30:23-04:00 INFO bidengine-order bid complete {"oid": "akash1.../42/1/1", "price": "1uakt"}
```

`json`:

```json
{"level":"info","time":"2026-10-12T12:30:23-04:00","module":"bidengine-order","message":"bid complete","oid":"akash1.../42/1/1","price":"1uakt"}
```

## Keys

All keys are `snake_case`. A unit test (`tools/logging/keys_guard_test.go`) fails on non-canonical keys.
`tools/logging/module_audit_test.go` fails when a log call goes through a logger taken from the context
(`ctxlog.LogcFromCtx`) or created by `logging.New` without a `module` bound by `With`.
Another (`tools/logging/entity_ids_test.go`) fails when an ID is logged under a key other than its own,
an ID key carries a value of another type, or an ID key is already bound by the logger's `With`.
Logging an ID is never required.

| Key           | Meaning                                                         |
|---------------|-----------------------------------------------------------------|
| `module`      | Component emitting the entry. Exactly one per entry; set via `With`. |
| `err`         | Error                                                           |
| `lid`         | Lease ID (`owner/dseq/gseq/oseq/provider`)                      |
| `oid`         | Order ID (`owner/dseq/gseq/oseq`)                               |
| `did`         | Deployment ID (`owner/dseq`)                                    |
| `bid`         | Bid ID                                                          |
| `gid`         | Group ID (`owner/dseq/gseq`)                                    |
| `ns`          | Kubernetes namespace                                            |
| `owner`       | Tenant (deployment owner) account address                       |
| `provider`    | Provider account address                                        |
| `service`     | Service name                                                    |
| `hostname`    | Hostname                                                        |
| `count`       | Number of items                                                 |
| `group_name`  | Deployment group name                                           |
| `listen_addr` | Listen address of a server                                      |

Renamed keys:

| Old                                                              | New                |
|------------------------------------------------------------------|--------------------|
| `lease`, `lease-id`, `leaseID`, `leaseId`, `lease_id`, `LeaseID` | `lid`              |
| `order`, `order-id`, `orderID`                                   | `oid`              |
| `deployment`                                                     | `did`              |
| `group`                                                          | `gid`              |
| `error`                                                          | `err`              |
| `namespace`, `lease-ns`, `lease namespace`                       | `ns`               |
| `addr` (owner / provider / listener)                             | `owner` / `provider` / `listen_addr` |
| `address` (REST listener / PCI device)                           | `listen_addr` / `pci_addr` |
| `provider` (gateway implementation), `gateway-provider`          | `gateway_provider` |
| `service-name`, `serviceName`                                    | `service`          |
| `host`                                                           | `hostname`         |
| `cnt`, `qty`, `quantity`                                         | `count`            |
| `group-name`, `manifest-group`, `resource-group`                 | `group_name`       |
| `cmp`, `component`, `op`, `operator`                             | `module`           |
| any other kebab-case / camelCase key                             | snake_case         |

## Modules

Module names changed where a different key was used before:

| Before                         | `module`                                                       |
|--------------------------------|----------------------------------------------------------------|
| `component=k8s`                | `k8s`                                                          |
| `cmp=deployment-monitor`, `cmp=deployment-manager`, `cmp=inventory-service`, `cmp=service-discovery-agent`, `cmp=balance-checker`, `cmp=waiter` | same value |
| `module=provider-cluster cmp=service` | `provider-cluster`                                      |
| `cmp=provider` (kubeconfig)    | `kubeconfig`                                                   |
| `component=storage` (cert issuer) | `cert-issuer-storage`                                       |
| `operator=ip`, `op=hostname`   | `operator-ip`, `operator-hostname`                             |
| inventory operator (logr names) | `operator-inventory`, `operator-inventory.<name>` (e.g. `operator-inventory.nodes`) |
| operator clients in `run`      | `operator-inventory-client`, `operator-ip-client`, `operator-hostname-client` |
| `migrate run`                  | `migrations`                                                   |
| hardware discovery pods (`tools psutil serve`) | `psutil`                                       |
| `client=kube` (kube client)    | `kube-client` in `run`, `operator-ip.kube-client` in `operator ip` |
| none (`run` itself)            | `provider`                                                     |
| none (REST / gRPC gateway)     | `gateway-rest`, `gateway-grpc`                                 |

## Hardware discovery pods

`operator inventory` starts a hardware discovery pod (`provider-services tools psutil serve`) on every node.
The operator passes its own logging configuration to these pods as `AP_LOG_LEVEL`, `AP_LOG_FORMAT`,
`AP_LOG_COLOR`, `AP_LOG_TIMESTAMP` and `AP_TRACE`, so they log in the same format as the operator.

## Attestation sidecar

The sidecar reads the same options from `ATTESTATION_LOG_LEVEL`, `ATTESTATION_LOG_FORMAT`,
`ATTESTATION_LOG_COLOR`, `ATTESTATION_LOG_TIMESTAMP` and `ATTESTATION_TRACE` (same values and defaults).
The provider's attestation webhook injects them into every sidecar from the provider's own configuration,
so logging is configured once on the provider.

The sidecar is a separate Go module with its own copy of the logger (`sidecar/attestation/internal/logging`).
Change options, values, defaults or parsing in both copies: both test suites run
`tools/logging/testdata/conformance.json`, so a change made to one copy only fails the other's tests.

## Behavior changes

- `run` logs moved from stderr to stdout. CLI errors printed by cobra still go to stderr.
- Colors and RFC3339 timestamps with nanoseconds (`rfc3339nano`) are on by default, so entries logged
  within the same second keep their order. Set `AP_LOG_FORMAT=json` or `AP_LOG_COLOR=false`
  to keep ANSI codes out of `kubectl logs`.
- Default level of `operator ip|hostname` and `migrate run` is now `info` (previously everything was logged);
  `operator inventory` changed from `debug` to `info`.
- Log keys and module names changed (see above); IDs are rendered as strings.
- `log_format=text` is rejected.
- Top-level `provider-services ip` and `provider-services hostname` were removed; use
  `provider-services operator ip|hostname`.
- Operators shut down gracefully on SIGTERM and exit with code 0.
