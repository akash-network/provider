#!/usr/bin/env bash

parse_args() {
    while getopts "?x" arg; do
        case "$arg" in
            x) set -x ;;
            *)
                echo "invalid flag"
                exit 1
                ;;
        esac
    done
    shift $((OPTIND - 1))
}

if [[ "$SHELL" == "bash" ]]; then
    if [ "${BASH_VERSINFO:-0}" -lt 4 ]; then
        echo "the script needs BASH 4 or above" >&2
        exit 1
    fi
fi

is_command() {
    command -v "$1" >/dev/null
}

parse_args "$@"

if [[ "$OSTYPE" == "darwin"* ]]; then
    echo "Detected Darwin based system"

    if ! is_command brew; then
        echo "homebrew is not installed. visit https://brew.sh"
        exit 1
    fi

    tools=

    if ! is_command make || [[ $(make --version | head -1 | cut -d" " -f3 | cut -d"." -f1) -lt 4 ]]; then
        tools="$tools make"
    fi

    if ! brew list coreutils >/dev/null 2>&1 ; then
        tools="$tools coreutils"
    fi

    if ! brew list coreutils >/dev/null 2>&1 ; then
        tools="$tools coreutils"
    fi

    if ! brew list qemu >/dev/null 2>&1 ; then
        tools="$tools qemu"
    fi

    if ! is_command direnv; then
        tools="$tools direnv"
    fi

    if ! is_command unzip; then
        tools="$tools unzip"
    fi

    if ! is_command wget; then
        tools="$tools wget"
    fi

    if ! is_command curl; then
        tools="$tools curl"
    fi

    if ! is_command npm; then
        tools="$tools npm"
    fi

    if ! is_command jq; then
        tools="$tools jq"
    fi

    if ! is_command readlink; then
        tools="$tools readlink"
    fi

    if [[ "$tools" != "" ]]; then
        # don't put quotes around $tools!
        # shellcheck disable=SC2086
        brew install $tools
    else
        echo "All requirements already met. Nothing to install"
    fi
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    if is_command dpkg; then
        echo "Detected Debian based system"
        tools=
        if ! is_command make; then
            tools="$tools make"
        fi

        if ! dpkg -l build-essentials; then
            tools="$tools build-essentials"
        fi

        if ! is_command direnv; then
            tools="$tools direnv"
        fi

        if ! is_command unzip; then
            tools="$tools unzip"
        fi

        if ! is_command wget; then
            tools="$tools wget"
        fi

        if ! is_command curl; then
            tools="$tools curl"
        fi

        if ! is_command npm; then
            tools="$tools npm"
        fi

        if ! is_command jq; then
            tools="$tools jq"
        fi

        if ! is_command readlink; then
            tools="$tools coreutils"
        fi

        cmd="apt-get"

        if is_command sudo; then
            cmd="sudo $cmd"
        fi

        if [[ "$tools" != "" ]]; then
            $cmd update
            # don't put quotes around $tools!
            # shellcheck disable=SC2086
            (set -x; $cmd install -y $tools)
        else
            echo "All requirements already met. Nothing to install"
        fi
    fi
else
    echo "Unsupported OS $OSTYPE"
    exit 1
fi
