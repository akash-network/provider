# Dev Environment: "Kube" configuration

The _Kube_ dev environment builds:

* A single-node blockchain network
* An Akash Provider Services Daemon (PSD) for bidding and running workloads.
* A Kubernetes cluster for the PSD to run workloads on.

The [instructions](#runbook) below will illustrate how to run a network with a single, local node and execute workloads in [kind](https://kind.sigs.k8s.io/):

* [Initialize blockchain node and client](#initialize)
* [Run a single-node network](#run-local-network)
* [Query objects on the network](#run-query)
* [Create a provider](#create-a-provider)
* [Run provider services](#run-provider-services)
* [Create a deployment](#create-a-deployment)
* [Bid on an order](#create-a-bid)
* [Terminate a lease](#terminate-lease)

## Setup

Four keys and accounts are created.  The key names are:

| Key Name    | Use                                              |
|-------------|--------------------------------------------------|
| `main`      | Primary account (creating deployments, etc...)   |
| `provider`  | The provider account (bidding on orders, etc...) |
| `validator` | The sole validator for the created network       |
| `other`     | Misc. account to (receives tokens, etc...)       |

Most `make` commands are configurable and have defaults to make it
such that you don't need to override them for a simple pass-through of
this example.

| Name                | Default           | Description                                                  |
|---------------------|-------------------|--------------------------------------------------------------|
| `KEY_NAME`          | `main`            | tenant key; also the identity used for provider gateway calls |
| `PROVIDER_KEY_NAME` | `provider`        | name of key to use for provider                              |
| `SDL_PATH`          | `deployment.yaml` | SDL used by `deployment-create`, `send-manifest`, ...        |
| `DSEQ`              | 1                 | deployment sequence                                          |
| `GSEQ`              | 1                 | group sequence                                               |
| `OSEQ`              | 1                 | order sequence                                               |
| `PRICE`             | 10uakt            | price to bid                                                 |
| `DEPOSIT`           | (empty)           | deployment deposit, e.g. `5000000uact`; empty uses the chain's uact minimum |

Client commands (transactions, queries, provider gateway calls) run through
[`akt`](https://github.com/akash-network/akt), installed into `.cache/bin` by
`make init`. `make init` creates an akt context named `local` in `AKASH_HOME`
(`AKT_HOME` points at the same directory), so akt shares the keyring,
certificates and genesis with the `akash` node and `provider-services`. Targets
other than `make init` refuse to run until that context exists.

Things that differ from the previous `akash`/`provider-services` commands:

- `query-deployments`, `query-orders`, `query-bids`, `query-leases` and
  `query-certificates` list entries of the `KEY_NAME` account only, not of
  every owner.
- Deployments are funded in `uact`. SDLs priced in `uakt` (for example
  [`grafana.yaml`](grafana.yaml)) cannot be deployed on the current chain:
  without `DEPOSIT` akt rejects them with `Mismatched denominations (uact != uakt): Deposit invalid`,
  and with a `uakt` deposit the chain rejects the transaction (`Deposit invalid`).
  Use `uact` pricing, as in [`deployment.yaml`](deployment.yaml).
- The provider assumes `amd64` for SDLs that do not set a CPU architecture. On
  an arm64 host (e.g. kind on Apple Silicon) add `attributes: {arch: arm64}`
  under `resources.cpu`, otherwise the provider reports insufficient capacity
  and does not bid.

# Runbook

The following steps will bring up a network and allow for interacting
with it.

Running through the entire runbook requires three terminals.
Each command is marked __t1__-__t3__ to indicate a suggested terminal number.

If at any time you'd like to start over with a fresh chain, simply run:

__t1 run__
```sh
make clean kube-cluster-delete
make init
```

## Initialize

Start and initialize kind.

Exposing tenant workloads presents some difficulties for creating development
environments.  Two options are offered below - the first (random port) is less error-prone
and can have multiple instances run concurrently, while the second option arguably
has a better payoff.

**note**: this step waits for Kubernetes metrics to be available, which can take some time.
The counter on the left side of the messages is regularly in the 120 range.  If it goes beyond 250,
there may be a problem.


| Option                                            | __t1 Step: 1__                                             | Explanation                                                                                                                                                        |
|---------------------------------------------------|------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Map random local port to port 80 of your workload | `make kind-cluster-setup`                                  | This is less error-prone, but makes it difficult to access your app through the browser.                                                                          |
| Map localhost port 80 to workload                 | `KIND_CONFIG=kind-config-80.yaml make kind-cluster-create` | If anything else is listening on port 80 (any other web server), this method will fail.  If it does succeed, you will be able to browse your app from the browser. |

### Gateway

Tenant traffic goes through the Gateway API `akash-gateway` Gateway. `GATEWAY_IMPL`
selects the implementation:

| `GATEWAY_IMPL`    | Implementation                                                                                                                                                       |
|-------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `envoy` (default) | Envoy Gateway in GatewayNamespace mode, installed from its pinned release manifest. Envoy runs as a DaemonSet on the node's host ports, as on providers, so it uses the default kind config. |
| `nginx`           | NGINX Gateway Fabric, exposed through NodePorts mapped by `kind-config-gateway.yaml` (ports 8080 and 8443). Kept for one release as the rollback path.               |

For example, `GATEWAY_IMPL=nginx make kube-cluster-setup`. The hostname operator detects
the implementation from the Gateway's GatewayClass (`--gateway-provider=auto`).

You can verify the Gateway is ready:
```sh
kubectl get gateway akash-gateway -n akash-gateway
```

Expected output:
```
NAME            CLASS   ADDRESS        PROGRAMMED   AGE
akash-gateway   envoy   10.233.x.x     True         1m
```

## Build Akash binaries and initialize network

Initialize keys and accounts:

### __t1 Step: 2__
```sh
make init
```

## Run local network

In a separate terminal, the following command will run the `akash` node:

### __t2 Step: 3__
```sh
make node-run
```

You can check the status of the network with:

__t1 status__
```sh
make node-status
```

You should see blocks being produced - the block height should be increasing.

You can now view genesis accounts that were created:

__t1 status__
```sh
make query-accounts
```

## Create a provider

Create a provider on the network with the following command:

### __t1 Step: 4__
```sh
make provider-create
```

View the on-chain representation of the provider with:

__t1 status__
```sh
make query-provider
```

## Run Provider

To run Provider as a simple binary connecting to the cluster, in a third terminal, run the command:

### __t3 Step: 5__

```sh
make provider-run
```

The provider attaches tenant routes to `akash-gateway` in the `akash-gateway` namespace
(`--gateway-name` and `--gateway-namespace`).

Query the provider service gateway for its status:

__t1 status__
```sh
make provider-status
```

When using Gateway API mode, deployments will create `HTTPRoute` resources instead of `Ingress` resources. You can verify this with:

```sh
kubectl get httproutes -A
```

## Create a deployment

Create a deployment from the `main` account with:

### __t1 run Step: 6__
```sh
make deployment-create
```

This particular deployment is created from the sdl file in this directory ([`deployment.yaml`](deployment.yaml)).

Check that the deployment was created.  Take note of the `dseq` - deployment sequence:

__t1 status__
```sh
make query-deployments
```

After a short time, you should see an order created for this deployment with the following command:

```sh
make query-orders
```

The Provider Services Daemon should see this order and bid on it.

```sh
make query-bids
```

When a bid has been created, you may create a lease:


### __t1 run Step: 7__

To create a lease, run

```sh
make lease-create
```

You can see the lease with:

```sh
make query-leases
```

You should now see "pending" inventory in the provider status:

```sh
make provider-status
```

## Distribute Manifest

Now that you have a lease with a provider, you need to send your
workload configuration to that provider by sending it the manifest:

### __t1 Step: 8__
```sh
make send-manifest
```

You can check the status of your deployment with:

__t1 status__
```sh
make provider-lease-status
```

You can reach your app with the following (Note: `Host:` header tomfoolery abound)

__t1 status__
```sh
make provider-lease-ping
```

Get service status

__t1 service status__
```sh
make provider-lease-status
```

Fetch logs from deployed service (all pods)

__t1 service logs__
```sh
make provider-lease-logs
```

If you chose to use port 80 when setting up kind, you can browse to your
deployed workload at http://hello.localhost

## Update Deployment

Updating active Deployments is a two step process. First edit the `deployment.yaml` with whatever changes are desired. Example; update the `image` field.
 1. Update the Akash Network to inform the Provider that a new Deployment declaration is expected.
   * `make deployment-update`
 2. Send the updated manifest to the Provider to run.
   * `make send-manifest`

Between the first and second step, the prior deployment's containers will continue to run until the new manifest file is received, validated, and new container group operational. After health checks on updated group are passing; the prior containers will be terminated.

#### Limitations

Akash Groups are translated into Kubernetes Deployments, this means that only a few fields from the Akash SDL are mutable. For example `image`, `command`, `args`, `env` and exposed ports can be modified, but compute resources and placement criteria cannot.

## HTTP options

The hostname operator renders each hostname's SDL `http_options` for the Gateway's
implementation:

| Implementation | Rendered as |
|----------------|-------------|
| Envoy Gateway  | One or two `BackendTrafficPolicy` objects per route (timeouts, connect timeout, retries). Retries that can fire after the backend received a request apply to idempotent methods only, unless `next_cases` includes `non_idempotent`. Body size and proxy buffer options aren't applied. |
| NGINX Gateway Fabric | A `SnippetsFilter` of nginx directives, referenced from the route. |

To remove the cluster:

```sh
make kube-cluster-delete
```

## Terminate lease

There are a number of ways that a lease can be terminated.

#### Provider closes the bid:

__t1 teardown__
```sh
make bid-close
```

#### Tenant closes the lease

__t1 teardown__
```sh
make lease-close
```

#### Tenant pauses the group

__t1 teardown__
```sh
make group-pause
```

#### Tenant closes the group

__t1 teardown__
```sh
make group-pause
```

#### Tenant closes the deployment

__t1 teardown__
```sh
make deployment-close
```
