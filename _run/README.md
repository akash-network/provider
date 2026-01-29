# Akash Development Environment

Follow these sequential steps to build a local Akash development environment.

* [Overview and Requirements](#overview-section)
* [Code](#code-section)
* [Install Tools](#install-tools-section)
* [Development Environment General Behavior](#general-behavior-section)
* [Runbook Overview](#runbook-overview-section)
* [Parameters](#parameters-section)
* [Use Runbook - Local Kind Cluster (kube)](#use-runbook-kube)
* [Use Runbook - Remote SSH Cluster (ssh)](#use-runbook-ssh)

## <a id="overview-section"></a>Overview and Requirements

### Overview

This page covers setting up development environment for both [node](https://github.com/akash-network/node) and [provider](https://github.com/akash-network/provider) repositories. The provider repo elected as placeholder for all the scripts as it depends on the `node` repo.   Should you already know what this guide is all about - feel free to explore examples.

### Requirements

#### Golang

Go must be installed on the machine used to initiate the code used in this guide. Both projects - Akash Node and Provider - are keeping up-to-date with major version on development branches. Both repositories are using the latest version of the Go, however only minor that has to always match.

#### **Docker Engine**

Ensure that Docker Desktop/Engine has been installed on machine that the development environment will be launched from.

#### Direnv Use

##### Install Direnv if Necessary

Direnv is used for the install process.  Ensure that you have Direnv install these [instructions](https://direnv.net/).

##### Configure Environment for Direnv Use

* Edit the ZSH shell profile with visual editor.

```
vi .zshrc
```

* Add the following line to the profile.

```
eval "$(direnv hook zsh)"
```

## <a id="code-section"></a>Code

In the example use within this guide, repositories will be located in `~/go/src/github.com/akash-network`. Create directory if it does not already exist via:

```
mkdir -p ~/go/src/github.com/akash-network
```

### Clone Akash Node and Provider Repositories

> _**NOTE**_ - all commands in the remainder of this guide  assume a current directory of `~/go/src/github.com/akash-network`unless stated otherwise.

```shell
cd ~/go/src/github.com/akash-network 
git clone https://github.com/akash-network/node.git
git clone https://github.com/akash-network/provider.git
```

## <a id="install-tools-section"></a>Install Tools

Run following script to install all system-wide tools. Currently supported host platforms.

* MacOS
* Debian based OS PRs with another hosts are welcome
* Windows is not supported

```shell
cd ~/go/src/github.com/akash-network
./provider/script/install_dev_dependencies.sh
```

## <a id="general-behavior-section"></a>Development Environment General Behavior

All examples are located within [\_run](https://github.com/akash-network/provider/tree/main/\_run) directory. Commands are implemented as `make` targets.

There are three ways we use to set up the Kubernetes cluster.

* kind
* minukube
* ssh

Both `kind` and `minikube` are e2e, i.e. the configuration is capable of spinning up cluster and the local host, whereas `ssh` expects cluster to be configured before use.

## <a id="runbook-overview-section"></a>Runbook Overview

There are four configuration variants, each presented as directory within[ \_run](https://github.com/akash-network/provider/tree/main/\_run).

* `kube` - uses `kind` to set up local cluster. It is widely used by e2e testing of the provider. Provider and the node run as host services. All operators run as kubernetes deployments.
* `single` - uses `kind` to set up local cluster. Main difference is both node and provider (and all operators) are running within k8s cluster as deployments. (at some point we will merge `single` with `kube` and call it `kind`)
* `minikube` - not in use for now
* `ssh` - expects cluster to be up and running. mainly used to test sophisticated features like `GPU` or `IP leases`

The only difference between environments above is how they set up. Once running, all commands are the same.

This guide covers the two most frequently used development paths: **Local Kind Cluster (`kube`)** and **Remote SSH Cluster (`ssh`)**. Running through either runbook requires multiple terminals. Each command is marked **t1**-**t3** to indicate a suggested terminal number.

## <a id="parameters-section"></a>Parameters

Parameters for use within the Runbooks detailed later in this guide.

| Name                | Default value                                                                                     | Effective on target(s)                                                                                                                                            |
| ------------------- | ------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| SKIP\_BUILD         | false                                                                                             |                                                                                                                                                                   |
| DSEQ                | 1                                                                                                 | <ul><li>deployment-\* </li><li>lease-\* </li><li>bid-\* </li><li>send-manifest</li></ul>                                                                             |
| OSEQ                | 1                                                                                                 | <ul><li>deployment-\* </li><li>lease-\* </li><li>bid-\* </li><li>send-manifest</li></ul>                                                                             |
| GSEQ                | 1                                                                                                 | <ul><li>deployment-\* </li><li>lease-\* </li><li>bid-\* </li><li>send-manifest</li></ul>                                                                             |
| KUSTOMIZE\_INSTALLS | <p>Depends on runbook.<br/>Refer to each runbook's <code>Makefile</code> to see default value.</p> | <ul><li>kustomize-init</li><li>kustomize-templates</li><li>kustomize-set-images</li><li>kustomize-configure-services </li><li>kustomize-deploy-services</li></ul> |

## <a id="use-runbook-kube"></a>Use Runbook - Local Kind Cluster (kube)

See [kube/README.md](./kube/README.md) for detailed instructions on setting up a local Kind cluster development environment.

## <a id="use-runbook-ssh"></a>Use Runbook - Remote SSH Cluster (ssh)

See [ssh/README.md](./ssh/README.md) for detailed instructions on connecting to a remote Kubernetes cluster via SSH for development.
