# Remote SSH Cluster Development Environment

## Overview

The SSH runbook connects to a pre-existing remote Kubernetes cluster via SSH and kubeconfig access. This approach is primarily used for testing sophisticated features like GPU workloads or IP leases that may require specific hardware or network configurations.

**Important**: In the SSH development environment, the **Akash node and provider services run locally** on your machine, while the **remote Kubernetes cluster is used for hosting deployments and workloads**. This hybrid approach allows you to develop and debug Akash services locally while leveraging remote cluster resources for actual workload execution.

## Prerequisites

Before starting this runbook, you must have:

1. **A running Kubernetes cluster** accessible via SSH
2. **SSH key-based authentication** configured for the cluster nodes
3. **Kubectl access** to the cluster from your local machine
4. **Root or sudo access** on the cluster nodes for container image management

## Remote Cluster Setup

### STEP 1 - Configure SSH Access

Set up passwordless SSH access to your remote cluster:

```bash
# Edit SSH config to specify key and user
vim ~/.ssh/config

# Add entry for your cluster
Host <YOUR_CLUSTER_IP>
    IdentityFile ~/.ssh/your-key
    User root
    IdentitiesOnly yes

# Test SSH access
ssh <YOUR_CLUSTER_IP> "kubectl get nodes"
```

### STEP 2 - Prepare Remote Cluster

Ensure your remote cluster has the required container management tools:

```bash
# SSH into your remote cluster node
ssh root@<YOUR_CLUSTER_IP>

# Install required packages
apt-get update
apt-get install -y uidmap slirp4netns

# Install nerdctl for container image management
curl -sSL https://github.com/containerd/nerdctl/releases/download/v1.7.2/nerdctl-1.7.2-linux-amd64.tar.gz | tar Cxzv /usr/local/bin/

# Verify nerdctl works with your cluster's containerd
nerdctl version

# Label the node for ingress controller scheduling (required for single-node clusters)
kubectl get nodes  # Note the node name
kubectl label node <NODE_NAME> ingress-ready=true

# Verify the label was added
kubectl get nodes --show-labels | grep ingress-ready
```

### STEP 3 - Configure Local Kubeconfig Access

Set up kubectl access to your remote cluster:

```bash
# SSH into your remote cluster
ssh root@<YOUR_CLUSTER_IP>

# First, update the API server to advertise the external IP
sudo vim /etc/kubernetes/manifests/kube-apiserver.yaml

# Find this line:
# - --advertise-address=<INTERNAL_IP>
# Change it to:
# - --advertise-address=<YOUR_CLUSTER_IP>

# Wait for the API server pod to restart (check with: kubectl get pods -n kube-system)
# This may take a minute or two

# Once restarted, create a kubeconfig with external IP
kubectl config view --raw > /tmp/external-kubeconfig.yaml

# Update the server URL to use external IP
sed -i 's|https://127\.0\.0\.1:6443|https://<YOUR_CLUSTER_IP>:6443|' /tmp/external-kubeconfig.yaml

# Example:
# sed -i 's|https://127\.0\.0\.1:6443|https://43.57.209.6:6443|' /tmp/external-kubeconfig.yaml

# Check the cluster name in the kubeconfig
grep "name:" /tmp/external-kubeconfig.yaml

# Configure to skip TLS verification using the correct cluster name (typically cluster.local)
kubectl config set-cluster cluster.local --insecure-skip-tls-verify=true --kubeconfig /tmp/external-kubeconfig.yaml

# Verify the configuration works on the remote host
KUBECONFIG=/tmp/external-kubeconfig.yaml kubectl get nodes

# Exit back to your local machine
exit
```

Now copy the working kubeconfig to your local machine:

```bash
# Copy the working kubeconfig from remote cluster
scp -i ~/.ssh/your-key root@<YOUR_CLUSTER_IP>:/tmp/external-kubeconfig.yaml ~/.kube/remote-cluster-config

# Set KUBECONFIG environment variable
export KUBECONFIG=~/.kube/remote-cluster-config

# Test cluster access from local machine
kubectl get nodes
```

## Local Environment Setup

### STEP 4 - Navigate to SSH Directory and Configure Environment

```bash
cd ~/go/src/github.com/akash-network/provider/_run/ssh
```

Configure the required environment variables:

```bash
# Edit .envrc
vim .envrc

# Ensure it contains:
source_up .envrc

dotenv_if_exists dev.env

AP_RUN_NAME=$(basename "$(pwd)")
AP_RUN_DIR="${DEVCACHE_RUN}/${AP_RUN_NAME}"

export AKASH_HOME="${AP_RUN_DIR}/.akash"
export AKASH_KUBECONFIG=$KUBECONFIG
export AP_KUBECONFIG=$KUBECONFIG
export AP_RUN_NAME
export AP_RUN_DIR
export KUBE_SSH_NODES="root@<YOUR_CLUSTER_IP>"

# Reload direnv
direnv reload

# Verify required variables are set
echo "AP_RUN_NAME: $AP_RUN_NAME"
echo "KUBE_SSH_NODES: $KUBE_SSH_NODES"
```

### STEP 5 - Address Known Issues

#### Fix Goreleaser Version Issue

```bash
# Set the correct goreleaser version based on your Go installation
export GOVERSION_SEMVER=v1.24.2
```

#### Fix Rootless Containerd Issue

The setup script may fail when trying to install rootless containerd as root. Fix this by modifying the setup script:

```bash
# Create backup and modify setup script
cp ~/go/src/github.com/akash-network/provider/script/setup-kube.sh ~/go/src/github.com/akash-network/provider/script/setup-kube.sh.backup

# Comment out the problematic rootless setup line
sed -i.bak "s/ssh -n \"\$node\" 'containerd-rootless-setuptool.sh install'/# ssh -n \"\$node\" 'containerd-rootless-setuptool.sh install' # Commented out - using system containerd/" ~/go/src/github.com/akash-network/provider/script/setup-kube.sh
```

#### Fix Missing Makefile Target

The SSH Makefile is missing required targets. Add them using these simple commands:

```bash
# Add the missing kube-deployments-rollout target to SSH Makefile
echo -e "\n.PHONY: kube-deployments-rollout\nkube-deployments-rollout:" >> ~/go/src/github.com/akash-network/provider/_run/ssh/Makefile

# Add the missing kube-setup-ssh target to SSH Makefile
echo -e "\n.PHONY: kube-setup-ssh\nkube-setup-ssh:" >> ~/go/src/github.com/akash-network/provider/_run/ssh/Makefile
```

#### Fix Provider Kubeconfig

The provider service needs to use the correct kubeconfig for the remote cluster:

```bash
# Add kubeconfig flag to provider-run command
sed -i '' 's/--cluster-k8s \\/--cluster-k8s \\\
		--kubeconfig "$(AKASH_KUBECONFIG)" \\/' ~/go/src/github.com/akash-network/provider/_run/ssh/Makefile
```

#### Fix GPU Target Missing Steps

The SSH Makefile's GPU target is missing critical setup steps that are present in the standard setup. This fix ensures the GPU path includes namespace creation, ingress controller setup, and other required components:

```bash
# Add missing setup steps to GPU target
sed -i '' 's/kube-cluster-setup-gpu: init \\/kube-cluster-setup-gpu: init \\\
	$(KUBE_CREATE) \\\
	kube-cluster-check-info \\\
	kube-setup-ingress \\/' ~/go/src/github.com/akash-network/provider/_run/ssh/Makefile

# Add helm charts installation to GPU target
sed -i '' 's/kube-deployments-rollout \\/kube-deployments-rollout \\\
	kube-install-helm-charts \\/' ~/go/src/github.com/akash-network/provider/_run/ssh/Makefile

# Fix GPU target to use correct SSH setup target instead of kind target
sed -i '' 's/kind-setup-$(KIND_NAME)/kube-setup-$(AP_RUN_NAME)/' ~/go/src/github.com/akash-network/provider/_run/ssh/Makefile
```

## Cluster Initialization

### STEP 6 - Initialize Environment

```bash
# Initialize the Akash environment
make init
```

### STEP 7 - Set Up Remote Cluster

```bash
# Set required environment variables
export GOVERSION_SEMVER=v1.24.2
export KUBE_SSH_NODES="root@<YOUR_CLUSTER_IP>"

# Set up the cluster (may require extended timeout for ingress controller)
KUBE_ROLLOUT_TIMEOUT=300 make kube-cluster-setup
```

## GPU Support

The SSH runbook supports both standard and GPU workloads:

### Standard Setup
```bash
# Use the default target for non-GPU workloads
KUBE_ROLLOUT_TIMEOUT=300 make kube-cluster-setup
```

### GPU Setup
```bash
# Use the GPU-specific target for GPU workloads
KUBE_ROLLOUT_TIMEOUT=300 make kube-cluster-setup-gpu
```

> **Note**: GPU support requires appropriate GPU drivers and container runtime configuration on your remote cluster nodes.

## Provider Operations

Once the cluster setup is complete, you can proceed with the standard Akash provider workflow:

### STEP 8 - Start Akash Node

> _**NOTE**_ - run this command in terminal2

First, ensure the environment is properly loaded:

```bash
# Navigate to SSH directory and set kubeconfig
cd ~/go/src/github.com/akash-network/provider/_run/ssh
export KUBECONFIG=~/.kube/remote-cluster-config
direnv reload

# Verify variables are set
echo "KUBECONFIG: $KUBECONFIG"
echo "AKASH_KUBECONFIG: $AKASH_KUBECONFIG"
echo "AP_RUN_NAME: $AP_RUN_NAME"

# Start the node
make node-run
```

### STEP 9 - Create and Run Provider

> _**NOTE**_ - run these commands in separate terminals

```bash
# Terminal 1: Create provider (ensure environment is loaded first)
cd ~/go/src/github.com/akash-network/provider/_run/ssh
export KUBECONFIG=~/.kube/remote-cluster-config
direnv reload

# Verify variables are set
echo "KUBECONFIG: $KUBECONFIG"
echo "AKASH_KUBECONFIG: $AKASH_KUBECONFIG"

make provider-create

# Terminal 3: Run provider (ensure environment is loaded first)
cd ~/go/src/github.com/akash-network/provider/_run/ssh
export KUBECONFIG=~/.kube/remote-cluster-config
direnv reload

# Verify variables are set
echo "KUBECONFIG: $KUBECONFIG"
echo "AKASH_KUBECONFIG: $AKASH_KUBECONFIG"

# Start the provider
make provider-run
```

### STEP 10 - Test Deployment Workflow

> _**NOTE**_ - run these commands in terminal1

```bash
# Create test deployment
make deployment-create

# Query and verify
make query-deployments
make query-orders
make query-bids

# Create lease
make lease-create
make query-leases

# Send manifest and verify
make send-manifest
make provider-lease-status
make provider-lease-ping
```

## Troubleshooting

### Common Issues

1. **SSH Permission Denied**: Ensure SSH keys are properly configured and SSH agent is running
2. **Kubeconfig Access Issues**: Verify external IP access and firewall rules for port 6443
3. **Container Image Upload Failures**: Ensure nerdctl is properly installed on remote nodes
4. **Timeout Errors**: Use `KUBE_ROLLOUT_TIMEOUT=300` for slower network connections

To reset the environment:

```bash
make init
```

## Key Differences from Local Kind Cluster

- **No cluster creation**: Uses existing remote cluster
- **Dual access method**: Requires both SSH (for node operations) and kubeconfig (for Kubernetes API) access
- **Manual prerequisite setup**: Remote cluster must be prepared with required tools
- **Extended timeouts**: Network latency may require longer timeout values
- **Security considerations**: Production clusters should use proper TLS certificates instead of skipping verification
