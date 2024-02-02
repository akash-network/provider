#!/usr/bin/env bash

#
# Set up a kubernetes environment with kind.
#
# * Install Akash CRD
# * Install `akash-services` Namespace
# * Install Network Policies
# * Optionally install metrics-server

set -e

rootdir="$(dirname "$0")/.."

CRD_FILE=$rootdir/pkg/apis/akash.network/crd.yaml
timeout=10
retries=10
retrywait=1

usage() {
    cat <<EOF
Install k8s dependencies for integration tests against "KinD"

Usage: $0 [kind|ssh] $1 [crd|ns|metrics]
  kind:
    init:   init cluster configuration
    ns:     install akash namespace
    crd:    install the akash CRDs
  ssh:
    init:   init cluster configuration
    ns:     install akash namespace
    crd:    install the akash CRDs
  crd:            install the akash CRDs
  ns:             install akash namespace
  metrics:        install CRDs, NS, metrics-server and wait for metrics to be available
  calico-metrics: install CRDs, NS, Network Policies, metrics-server and wait for metrics to be available
  networking:     install essential k8s namespace and network policies for Akash services
  config-file:    print path to the config file depending on requested network type.
                  first argument - path to _run example, second argument - config name
                  allowed config names: default, calico
EOF
    exit 1
}

isuint() { [[ $1 =~ ^[0-9]+$ ]] ;}

short_opts=h
long_opts=help/crd:/retries:/retry-wait:/timeout:   # those who take an arg END with :

while getopts ":$short_opts-:" o; do
    case $o in
        :)
            echo >&2 "option -$OPTARG needs an argument"
            continue
            ;;
        '?')
            echo >&2 "bad option -$OPTARG"
            continue
            ;;
        -)
            o=${OPTARG%%=*}
            OPTARG=${OPTARG#"$o"}
            lo=/$long_opts/
            case $lo in
                *"/$o"[!/:]*"/$o"[!/:]*)
                    echo >&2 "ambiguous option --$o"
                    continue
                    ;;
                *"/$o"[:/]*)
                    ;;
                *)
                    o=$o${lo#*"/$o"};
                    o=${o%%[/:]*}
                    ;;
            esac

            case $lo in
                *"/$o/"*)
                    OPTARG=
                    ;;
                *"/$o:/"*)
                    case $OPTARG in
                        '='*)
                            OPTARG=${OPTARG#=}
                            ;;
                        *)
                            eval "OPTARG=\$$OPTIND"
                            if [ "$OPTIND" -le "$#" ] && [ "$OPTARG" != -- ]; then
                                OPTIND=$((OPTIND + 1))
                            else
                                echo >&2 "option --$o needs an argument"
                                continue
                            fi
                            ;;
                    esac
                    ;;
            *) echo >&2 "unknown option --$o"; continue;;
            esac
    esac
    case "$o" in
        crd)
            CRD_FILE=$OPTARG
            ;;
        timeout)
            timeout=$OPTARG
            if ! isuint "$timeout" ; then
                echo >&2 "timeout option must be positive integer"
                exit 1
            fi
            ;;
        retries)
            retries=$OPTARG

            if ! isuint "$retries" ; then
                echo >&2 "retries option must be positive integer"
                exit 1
            fi
            ;;
        retry-wait)
            retrywait=$OPTARG
            if ! isuint "$retrywait" ; then
                echo >&2 "retry-wait option must be positive integer"
                exit 1
            fi
            ;;
    esac
done
shift "$((OPTIND - 1))"

if [[ $# -lt 2 ]]; then
    echo "invalid amount of args"
    exit 1
fi

install_ns() {
    set -x
    kubectl apply -f "$rootdir/_docs/kustomize/networking/namespace.yaml"
}

install_network_policies() {
    set -x
    kubectl kustomize "$rootdir/_docs/kustomize/akash-services/" | kubectl apply -f-
}

install_crd() {
    set -x
    kubectl apply -f "$CRD_FILE"
    kubectl apply -f "$rootdir/_docs/kustomize/storage/storageclass.yaml"
}

install_metrics() {
    set -x
    # https://github.com/kubernetes-sigs/kind/issues/398#issuecomment-621143252
    kubectl apply -f "$rootdir/_docs/kustomize/kind/kind-metrics-server.yaml"

    #  kubectl wait pod --namespace kube-system \
    #    --for=condition=ready \
    #    --selector=k8s-app=metrics-server \
    #    --timeout=90s

    echo "metrics initialized"
}

config_file() {
    if [ "$#" != "2" ]; then
        echo "invalid amount of args"
        usage
    fi

    _run_dir="$1"

    if [[ "$_run_dir" == */ ]]; then
        # shellcheck disable=SC2001
        _run_dir=$(echo "$_run_dir" | sed 's:/*$::')
    fi

    case "$2" in
        calico)
            file="$_run_dir/../kind-config-calico.yaml"
            ;;
        default)
            ;;&
        *)
            file="$_run_dir/kind-config.yaml"
            ;;
    esac

    file=$(realpath "$file")

    echo "$file"
    exit 0
}

command_ssh() {
    case "$1" in
    init)
        shift

        local nodes=("$1")

        # shellcheck disable=SC2048
        for node in ${nodes[*]}; do
            if ! ssh -n "$node" "test -e /etc/systemd/system/user@.service.d/delegate.conf"; then
                ssh -n "$node" 'sudo mkdir -p /etc/systemd/system/user@.service.d'
                ssh -n "$node" 'cat <<EOF | sudo tee /etc/systemd/system/user@.service.d/delegate.conf
[Service]
Delegate=cpu cpuset io memory pids
EOF'
                ssh -n "$node" 'sudo systemctl daemon-reload'
            fi

            local packages

            if ! ssh -n "$node" 'dpkg-query -W uidmap >/dev/null 2>&1'; then
                packages="$packages uidmap"
            fi

            if ! ssh -n "$node" 'dpkg-query -W slirp4netns >/dev/null 2>&1'; then
                packages="$packages slirp4netns"
            fi

            if [[ $packages != "" ]]; then
                ssh -n "$node" 'sudo apt-get install -y uidmap slirp4netns'
            fi
            ssh -n "$node" 'curl -sSL https://github.com/containerd/nerdctl/releases/download/v1.7.2/nerdctl-1.7.2-linux-$(uname -m | sed "s/x86_64/amd64/g").tar.gz | sudo tar Cxzv /usr/local/bin/'
            ssh -n "$node" 'curl -sSL https://github.com/rootless-containers/rootlesskit/releases/download/v2.0.0/rootlesskit-$(uname -m).tar.gz | sudo tar Cxzv /usr/local/bin/'
            ssh -n "$node" 'containerd-rootless-setuptool.sh install'
        done

        install_ns
        install_crd
        ;;
    *)
        echo "invalid command \"$1\""
        usage "$@"
        ;;
    esac
}

command_kind() {
    case "$1" in
    init)
        install_ns
        install_crd
        ;;
    *)
        echo "invalid command \"$1\""
        usage "$@"
        ;;
    esac
}

command_kustomize() {
    case "$1" in
    image)
        shift

        at=$1
        image=$2
            echo -e "- op: replace\n  path: /spec/template/spec/${at}/image\n  value: ${image}"
        ;;
    esac
}

command_load_images() {
    case "$1" in
    docker2ctr)
        shift

        local remotes=("$1")
        local images=("$2")

        # shellcheck disable=SC2048
        for remote in ${remotes[*]}; do
            if ! ssh "$remote" "which nerdctl" >/dev/null 2>&1; then
                echo "nerdctl is not installed on \"$remote\" node. run \"setup-kube.sh ssh init\" first"
                exit 1
            fi
        done

        # shellcheck disable=SC2048
        for image in ${images[*]}; do
            if ! docker image inspect "$image" >/dev/null 2>&1; then
                echo "image \"$image\" is not present locally"
                exit 1
            fi
        done

        # shellcheck disable=SC2048
        for remote in ${remotes[*]}; do
            for image in ${images[*]}; do
                docker save "$image" | tqdm --bytes --total "$(docker image inspect "$image" --format='{{.Size}}')" | ssh "$remote" "sudo nerdctl image load"
            done
        done

        ;;
    docker2kind)
        shift

        local kind_name=$1
        local images=("$2")

        # shellcheck disable=SC2048
        for image in ${images[*]}; do
            if ! docker image inspect "$image" >/dev/null 2>&1; then
                echo "image \"$image\" is not present locally"
                exit 1
            fi
        done

        # shellcheck disable=SC2048
        for image in ${images[*]}; do
            kind --name "${kind_name}" load docker-image "${image}"
        done

        ;;
    *)
        echo "invalid load images command \"$1\""
        usage "$@"
        ;;
    esac
}

command_upload() {
    case "$1" in
    crd)
        shift
        install_ns
        install_crd
        ;;
    images)
        shift
        command_load_images "$@"
        ;;
    *)
        echo "invalid upload command \"$1\""
        usage "$@"
    esac
}

wait_inventory_available() {
    set -x

    local pid

    kubectl -n akash-services port-forward --address 0.0.0.0 service/operator-inventory 8455:grpc &
    pid=$!

    # shellcheck disable=SC2064
    trap "kill -SIGINT ${pid}" EXIT

    timeout 10 bash -c -- 'while ! nc -vz localhost 8455 > /dev/null 2>&1 ; do sleep 0.1; done'

    local r=0

    while ! grpcurl -plaintext localhost:8455 akash.inventory.v1.ClusterRPC.QueryCluster | jq '(.nodes | length > 0) and (.storage | length > 0)' --exit-status > /dev/null 2>&1; do
        r=$((r+1))
        if [ ${r} -eq "${retries}" ]; then
            exit 0
        fi

        # shellcheck disable=SC2086
        sleep $retrywait
    done
}

wait() {
    case "${1}" in
    inventory-available)
        shift
        wait_inventory_available
        ;;
    *)
        echo "invalid wait command"
        usage "$@"
        ;;
    esac
}

case "${1}" in
ssh)
    shift
    command_ssh "$@"
    ;;
kind)
    shift
    command_kind "$@"
    ;;
kustomize)
    shift
    command_kustomize "$@"
    ;;
upload)
    shift
    command_upload "$@"
    ;;
"wait")
    shift
    wait "$@"
    ;;
*)
    echo "invalid cluster type"
    usage "$@"
    ;;
esac

