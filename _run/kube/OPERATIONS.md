# Additional Operations

## Update Deployment

Updating active Deployments is a two step process. First edit the `deployment.yaml` with whatever changes are desired. Example; update the `image` field.
 1. Update the Akash Network to inform the Provider that a new Deployment declaration is expected.
   * `make deployment-update`
 2. Send the updated manifest to the Provider to run.
   * `make send-manifest`

Between the first and second step, the prior deployment's containers will continue to run until the new manifest file is received, validated, and new container group operational. After health checks on updated group are passing; the prior containers will be terminated.

### Limitations

Akash Groups are translated into Kubernetes Deployments, this means that only a few fields from the Akash SDL are mutable. For example `image`, `command`, `args`, `env` and exposed ports can be modified, but compute resources and placement criteria cannot.

## Terminate Lease

There are a number of ways that a lease can be terminated.

### Provider closes the bid

```sh
make bid-close
```

### Tenant closes the lease

```sh
make lease-close
```

### Tenant pauses the group

```sh
make group-pause
```

### Tenant closes the group

```sh
make group-pause
```

### Tenant closes the deployment

```sh
make deployment-close
```
