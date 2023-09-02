### Resetting state
if at any point fresh start needed:
 - make sure node is not-running - `__t2` in the guide below
 - make sure provider is not-running - `__t3` in the guide below
 - `make clean`
 - `make kube-cluster-delete`


### SDL
SDL_PATH is set to `grafana.yaml`
it can be opted by setting SDL_PATH to custom SDL prior every make command

### Working dir
```shell
cd _run/kube
```

###  __t1 Step 1__
```shell
AKASH_DOCKER_IMAGE=ghcr.io/akash-network/node:0.22.9 DOCKER_IMAGE=ghcr.io/akash-network/provider:0.2.1 make prepare
```

### __t1 Step 2__
```shell
SKIP_BUILD=true \
AKASH_DOCKER_IMAGE=ghcr.io/akash-network/node:0.22.9 \
DOCKER_IMAGE=ghcr.io/akash-network/provider:0.2.1 \
CRD_FILE=https://raw.githubusercontent.com/akash-network/provider/v0.2.1/pkg/apis/akash.network/crd.yaml \
make kube-cluster-setup
```

### __t1 Step 3__
Ensure versions

#### akash expected to be v0.22.9
```shell
akash version
```
#### provider-services expected to be v0.2.1
```shell
provider-services version
```

### __t2 Step 4__ Run node
```shell
make node-run
```

### __t1 Step 5__ Create provider
```shell
make provider-create
```

### __t3 Step 6__ Run provider
```shell
make provider-run
```

### __t1 Step 7__ Create deployment
```shell
make deployment-create
make lease-create
make send-manifest
```

Ensure deployment has started

### __t1 Step 8__ Submit network upgrade
```shell
make send-upgrade
```
wait till node in __t2 fails with `CONSENSUS FAILURE UPGRADE REQUIRED`


### __t1 Step 9 Post Upgrade
```shell
make post-upgrade
```
Ensure versions

#### akash expected to be v0.24.0
```shell
akash version
```
#### provider-services expected to be v0.3.2-rcX
```shell
provider-services version
```

### __t2 Step 10__ Run node
```shell
make node-run
```

### __t3 Step 11__ Dry run migration
```shell
make provider-migrate

# should see smth like below
✓        loaded CRDs
✓        loaded active leases for provider "akash1muc4ckm26kyw975vffzp3a8ls34ew0n565ksyy"
✓        loaded CRDs
✓        backup manifests DONE: 1
✓        backup providers hosts DONE: 4
✓        migrated manifests to v2beta2
✓        migrated hosts to v2beta2

```

the tool does backup of all existing manifests into `.cache/run/kube/crd/v2beta1`
patched manifests are located in `.cache/run/kube/crd/v2beta2`

### __t3 Step 12__ Run migration
```shell
PROVIDER_MIGRATE_DRYRUN=false make provider-migrate
# will be asked to override backup. press Y
```

### __t1 Step 13__ Update operators
```shell
make kube-prepare-images kube-upload-images
make kustomize-init kustomize-deploy-services
```

### __t3 Step 14__ Start the provider
```shell
make provider-run
```

### Step 15 Inspect workload
Ensure provider pick up on upgraded manifests and workloads are up and running.


### Step 16 Perform deployment-update
make sure provider processes it and update deployment. make sure that manifest in kubernetes is updated as well
