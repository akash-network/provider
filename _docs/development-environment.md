# Setting-up development environment


This page covers setting up development environment for both [node](https://github.com/akash-network/node) and [provider](https://github.com/akash-network/provider) repositories.
The provider repository hosts the shared development scripts because it depends on the `node` repository.
Should you already know what this guide is all about - feel free to explore [examples](#how-to-use-runbook)

## Code

Checkout code if not done so into place of your convenience.
For this example, repositories will be located in `~/go/src/github.com/akash-network`

Checkout below assumes `git` is set to use SSH connection to GitHub

```shell
cd ~/go/src/github.com/akash-network # all commands below assume this as current directory
git clone git@github.com:akash-network/node
git clone git@github.com:akash-network/provider
```

## Requirements

- `Go` must be installed. Both projects are keeping up-to-date with major version on development branches.
  Both repositories are using the latest version of the Go, however only minor that has to always match.

### Install tools

Run following script to install all system-wide tools.
Currently supported host platforms:

- MacOS
- Debian based OS
  PRs with another hosts are welcome (except Windows)

```shell
./provider/script/install_dev_dependencies.sh
```

## How it works

### General behaviour

All examples are located within the [_run](https://github.com/akash-network/provider/tree/main/_run) directory.
[Commands](#commands) are implemented as `make` targets.

There are three ways we use to set up the k8s cluster.

- kind
- minikube
- ssh

Both `kind` and `minikube` are e2e, i.e. the configuration is capable of spinning up cluster and the local host, whereas `ssh` expects cluster to be configured before use.

### Runbook

There are four configuration variants, each presented as a directory within [_run](https://github.com/akash-network/provider/tree/main/_run).

- `kube` - uses `kind` to set up local cluster. It is widely used by e2e testing of the provider. Provider and the node run as host services. All operators run as kubernetes deployments.
- `single` - uses `kind` to set up local cluster. Main difference is both node and provider (and all operators) are running within k8s cluster as deployments. (at some point we will merge `single`
  with `kube` and call it `kind`)
- `minikube` - not in use for now
- `ssh` - expects cluster to be up and running. mainly used to test sophisticated features like `GPU` or `IP leases`

The only difference between environments above is how they set up. Once running, all commands are the same.

Running through the entire runbook requires multiples terminals.
Each command is marked __t1__-__t3__ to indicate a suggested terminal number.

If at any point something goes wrong and cluster needs to be run from the beginning:

```shell
cd _run/<kube|single|ssh>
make kube-cluster-delete
make clean
```

### Kustomize

TBD

#### Parameters

| Name                 |                                 Default value                                  | Effective on target(s)                                                                                                                   | Notes |
|:---------------------|:------------------------------------------------------------------------------:|------------------------------------------------------------------------------------------------------------------------------------------|-------|
| `SKIP_BUILD`         |                                    `false`                                     |                                                                                                                                          |
| `DSEQ`               |                                      `1`                                       | `deployment-*`<br/>`lease-*`<br/>`bid-*`<br/>`send-manifest`                                                                             |
| `OSEQ`               |                                      `1`                                       | `deployment-*`<br/>`lease-*`<br/>`bid-*`<br/>`send-manifest`                                                                             |
| `GSEQ`               |                                      `1`                                       | `deployment-*`<br/>`lease-*`<br/>`bid-*`<br/>`send-manifest`                                                                             |
| `KUSTOMIZE_INSTALLS` | Depends on runbook<br/>Refer to each runbook's `Makefile` to see default value | `kustomize-init`<br/>`kustomize-templates`<br/>`kustomize-set-images`<br/>`kustomize-configure-services`<br/>`kustomize-deploy-services` |       |

##### Keys

Each configuration creates four [keys](https://github.com/akash-network/provider/blob/main/_run/common.mk#L38-L43):
The keys are assigned to the targets and under normal circumstances there is no need to alter them. However, it can be done by setting `KEY_NAME`:

```shell
# create provider from **provider** key
make provider-create

# create provider from custom key
KEY_NAME=other make provider-create
```

#### How to use runbook

##### Kube

This runbook requires three terminals

1. Open runbook

   __all three terminals__
   ```shell
   cd _run/kube
   ```

2. Create and provision local kind cluster.

   __t1 run__
   ```shell
   make kube-cluster-setup
   ```
3. Start akash node

   __t2 run__
   ```shell
   make node-run
   ```
4. Create provider

   __t1 run__
   ```shell
   make provider-create
   ```

5. Start the provider

   __t3 run__
   ```shell
   make provider-run
   ```

6. Query the provider for its status

   __t1 run__
   ```shell
   make provider-status
   ```

7. __t1__ Create a deployment. Check that the deployment was created.  Take note of the `dseq` - deployment sequence:

   ```shell
   make deployment-create
   ```

   ```shell
   make query-deployments
   ```

   After a short time, you should see an order created for this deployment with the following command:

   ```shell
   make query-orders
   ```

   The Provider Services Daemon should see this order and bid on it.

   ```shell
   make query-bids
   ```

8. __t1 When a bid has been created, you may create a lease__

   To create a lease, run

   ```shell
   make lease-create
   ```

   You can see the lease with:

   ```shell
   make query-leases
   ```

   You should now see "pending" inventory in the provider status:

   ```shell
   make provider-status
   ```

9. __t1 Distribute Manifest__

   Now that you have a lease with a provider, you need to send your
   workload configuration to that provider by sending it the manifest:

   ```shell
   make send-manifest
   ```

   You can check the status of your deployment with:

   ```shell
   make provider-lease-status
   ```

   You can reach your app with the following (Note: `Host:` header tomfoolery abound)

   ```shell
   make provider-lease-ping
   ```

10. __t1 Get service status__

   ```sh
   make provider-lease-status
   ```

   Fetch logs from deployed service (all pods)

   ```sh
   make provider-lease-logs
   ```

##### Kube for e2e tests

This runbook requires two terminals

1. Open runbook

   __t1__
   ```shell
   cd _run/kube
   ```

2. Create and provision local kind cluster for e2e testing.

   __t1 run__
   ```shell
   make kube-cluster-setup-e2e
   ```

3. Run e2e tests

   ```shell
   make test-e2e-integration
   ```

##### Single

##### SSH
