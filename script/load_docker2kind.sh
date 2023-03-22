#!/usr/bin/env bash

images=("$1")
kind_name=$2

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
