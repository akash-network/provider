#!/usr/bin/env bash

images=("$1")

remote=$2

if ! ssh "$remote" "which nerdctl" >/dev/null 2>&1; then
    echo "nerdctl is not installed on remote server. https://github.com/containerd/nerdctl/blob/main/docs/rootless.md"
    exit 1
fi

# shellcheck disable=SC2048
for image in ${images[*]}; do
    if ! docker image inspect "$image" >/dev/null 2>&1; then
        echo "image \"$image\" is not present locally"
        exit 1
    fi
done

# need tqdm installed (https://github.com/tqdm/tqdm) for this to work.

# shellcheck disable=SC2048
for image in ${images[*]}; do
    docker save "$image" | tqdm --bytes --total "$(docker image inspect "$image" --format='{{.Size}}')" | ssh "$remote" "nerdctl image load"
done
