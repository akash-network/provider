# Integration Tests

This directory contains the integration and end-to-end (E2E) tests for the Akash Provider. These tests verify the functionality of the provider service in a real Kubernetes environment.

## Test Structure

The integration tests are organized into several test files:
- `e2e_test.go`: Main E2E test suite configuration
- `deployment_update_test.go`: Tests for deployment updates
- `jwtserver_test.go`: JWT server functionality tests
- `escrow_monitor_test.go`: Escrow monitoring tests
- `ipaddress_test.go`: IP address management tests
- And more specialized test files for various features

## Prerequisites

- Go 1.x or later
- Kubernetes cluster (local or remote)
- `kubectl` configured to access your cluster
- Access to pull container images from ghcr.io/akash-network

## Running the Tests

### 1. Setup the Test Environment

The tests must be run from one of the example directories in the `_run` folder. The `kube` directory is recommended for running tests:

```bash
cd _run/kube
```

### 2. Prepare the Kubernetes Cluster

Before running the tests, you need to set up the Kubernetes cluster with the necessary Custom Resource Definitions (CRDs) and components:

```bash
KUSTOMIZE_INSTALLS=akash-operator-inventory make kube-cluster-setup-e2e
```

This command will:
- Create the Kubernetes cluster if it doesn't exist
- Install Akash CRDs
- Set up required namespaces
- Configure necessary services
- Deploy operator components

### 3. Run the Tests

After the cluster is set up, you can run the integration tests:

```bash
make test-k8s-integration
```

TODO:

To run specific test files or test cases, you can use the `-run` flag with the Go test command:

```bash
make test-k8s-integration TESTS="-run TestE2EDeploymentUpdate"
```

## Cleanup

`make clean kube-cluster-delete-kind`

## Troubleshooting

### Common Issues

1. **CRD Not Found Error**
   If you see errors like "the server could not find the requested resource", ensure you've run the cluster setup command properly:
   ```bash
   KUSTOMIZE_INSTALLS=akash-operator-inventory make kube-cluster-setup-e2e
   ```

2. **Image Pull Errors**
   Ensure you have access to pull the required container images from ghcr.io/akash-network.

3. **Test Timeouts**
   Some tests might timeout in slower environments. You can increase the timeout using:
   ```bash
   make test-k8s-integration TESTS="-timeout 30m"
   ```

## Contributing

When adding new integration tests:

1. Use the `//go:build e2e` build tag for E2E test files
2. Follow the existing patterns in `e2e_test.go`
3. Add appropriate setup and teardown logic
4. Document any new requirements or dependencies
5. Ensure tests clean up resources after completion

## Additional Resources

- [Akash Provider Documentation](https://docs.akash.network)
- [Kubernetes Documentation](https://kubernetes.io/docs)
- [Go Testing Package Documentation](https://golang.org/pkg/testing)
