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

| Name                | Default    | Description                     |
|---------------------|------------|---------------------------------|
| `KEY_NAME`          | `main`     | standard key name               |
| `PROVIDER_KEY_NAME` | `provider` | name of key to use for provider |
| `DSEQ`              | 1          | deployment sequence             |
| `GSEQ`              | 1          | group sequence                  |
| `OSEQ`              | 1          | order sequence                  |
| `PRICE`             | 10uakt     | price to bid                    |

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

Note: The same cleanup command works for both NGINX Ingress and Gateway API modes.

## Initialize

Start and initialize kind.

Kubernetes ingress objects present some difficulties for creating development
environments.  Two options are offered below - the first (random port) is less error-prone
and can have multiple instances run concurrently, while the second option arguably
has a better payoff.

**note**: this step waits for Kubernetes metrics to be available, which can take some time.
The counter on the left side of the messages is regularly in the 120 range.  If it goes beyond 250,
there may be a problem.


| Option                                            | __t1 Step: 1__                                             | Explanation                                                                                                                                                        |
|---------------------------------------------------|------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Map random local port to port 80 of your workload | `make kind-cluster-setup`                                  | This is less error-prone, but makes it difficult to access your app through the browser. Configures hostname operator for NGINX Ingress mode.                      |
| Map localhost port 80 to workload                 | `KIND_CONFIG=kind-config-80.yaml make kind-cluster-create` | If anything else is listening on port 80 (any other web server), this method will fail.  If it does succeed, you will be able to browse your app from the browser. |

### Gateway API Setup (Optional)

As an alternative to NGINX Ingress, you can use Kubernetes Gateway API. NGINX Ingress will be EOL by March 2026, so Gateway API is the recommended approach for new deployments.

| Option                                   | __t1 Step: 1 (Gateway API)__     | Explanation                                                                                      |
|------------------------------------------|----------------------------------|--------------------------------------------------------------------------------------------------|
| Gateway API with NGINX Gateway Fabric    | `make kube-cluster-setup-gateway`| Creates kind cluster with Gateway API support, installs NGINX Gateway Fabric, and sets up all resources. Configures hostname operator for Gateway API mode. Maps ports 8080 (HTTP) and 8443 (HTTPS). |

This comprehensive setup target:
- Creates a kind cluster with port mappings for HTTP (8080) and HTTPS (8443)
- Installs Gateway API CRDs (v1)
- Installs NGINX Gateway Fabric controller
- Configures NodePort service for ports 31437 (HTTP) and 31438 (HTTPS)
- Creates the Gateway resource (`akash-gateway`)
- Configures the hostname operator for Gateway API mode
- Sets up all necessary Akash operators and services

You can verify the Gateway is ready:
```sh
kubectl get gateway akash-gateway -n akash-gateway
```

Expected output:
```
NAME            CLASS   CLUSTER-IP      ADDRESS        PROGRAMMED   AGE
akash-gateway   nginx   10.96.x.x       10.96.x.x      True         1m
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

**With NGINX Ingress (default):**
```sh
make provider-run
```

**With Gateway API:**
```sh
make provider-run-gateway
```

The Gateway API mode uses the following flags:
- `--ingress-mode=gateway-api` - Enable Gateway API instead of NGINX Ingress
- `--gateway-name=akash-gateway` - Name of the Gateway resource to use
- `--gateway-namespace=akash-gateway` - Namespace where the Gateway exists

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

## Gateway API vs NGINX Ingress

### Comparison

| Feature | NGINX Ingress (default) | Gateway API |
|---------|------------------------|-------------|
| Resource Type | `Ingress` | `HTTPRoute` |
| Class/Reference | `IngressClass: akash-ingress-class` | `Gateway: akash-gateway` |
| Annotations | `nginx.ingress.kubernetes.io/*` | `nginx.org/*` |
| EOL Status | March 2026 | Current standard |

### HTTP Options Translation

The provider automatically translates HTTP options from the SDL to the appropriate annotations:

| SDL Option | NGINX Ingress Annotation | Gateway API Annotation |
|------------|-------------------------|------------------------|
| Read Timeout | `nginx.ingress.kubernetes.io/proxy-read-timeout` | `nginx.org/proxy-read-timeout` |
| Send Timeout | `nginx.ingress.kubernetes.io/proxy-send-timeout` | `nginx.org/proxy-send-timeout` |
| Max Body Size | `nginx.ingress.kubernetes.io/proxy-body-size` | `nginx.org/client-max-body-size` |
| Next Upstream Tries | `nginx.ingress.kubernetes.io/proxy-next-upstream-tries` | `nginx.org/proxy-next-upstream-tries` |

### Cleanup

To remove the cluster (works for both NGINX Ingress and Gateway API):

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
