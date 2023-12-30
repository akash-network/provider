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

short_opts=h
long_opts=help/crd:   # those who take an arg END with :

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
*)
    echo "invalid cluster type"
    usage "$@"
    ;;
esac

#case "${1:-metrics}" in
#    crd)
#        install_crd
#        ;;
#    ns)
#        install_ns
#        ;;
#    metrics)
#        install_crd
#        install_ns
#        install_metrics
#        ;;
#    calico-metrics)
#        install_crd
#        install_ns
#        install_metrics
#        install_network_policies
#        ;;
#    networking)
#        install_ns
#        install_network_policies
#        ;;
#    config-file)
#        shift
#        config_file "$@"
#        ;;
#    *)
#        usage "$@"
#        ;;
#esac
