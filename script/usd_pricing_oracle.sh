#!/usr/bin/env bash
# shellcheck shell=bash

set -e

if [[ "$SHELL" == "bash" ]]; then
  if [ "${BASH_VERSINFO:-0}" -lt 4 ]; then
    echo "the script needs BASH 4 or above" >&2
    exit 1
  fi
fi

#  To run this script, the following commands need to be installed:
#
# * jq 1.5.1 or newer
# * bc
# * curl

if ! command -v jq &> /dev/null ; then
  echo "jq could not be found" >&2
  exit 1
fi

if ! command -v bc &> /dev/null ; then
  echo "bc could not be found" >&2
  exit 1
fi

# These are the variables one can modify to change the USD scale for each resource kind
CPU_UACT_SCALE=0.10
GPU_UACT_SCALE=0.50
MEMORY_UACT_SCALE=0.02
ENDPOINT_UACT_SCALE=0.02

declare -A STORAGE_UACT_SCALE

STORAGE_UACT_SCALE[ephemeral]=0.01
STORAGE_UACT_SCALE[default]=0.02
STORAGE_UACT_SCALE[beta1]=0.02
STORAGE_UACT_SCALE[beta2]=0.03
STORAGE_UACT_SCALE[beta3]=0.04
STORAGE_UACT_SCALE[ram]=0.02 # ram storage class is for tmp disks like /dev/shm, making assumption for now pricing is same of for regular RAM

# used later for validation
MAX_INT64=9223372036854775807

# local variables used for calculation
memory_total=0
cpu_total=0
gpu_total=0
endpoint_total=0
storage_cost_uact=0

# read the JSON in `stdin` into $script_input
read -r script_input

precision=$(jq -r '.price_precision // 6' <<<"$script_input")

# iterate over all the groups and calculate total quantity of each resource
for group in $(jq -c '.resources[]' <<<"$script_input"); do
  count=$(jq '.count' <<<"$group")

  memory_quantity=$(jq '.memory' <<<"$group")
  memory_quantity=$((memory_quantity * count))
  memory_total=$((memory_total + memory_quantity))

  cpu_quantity=$(jq '.cpu' <<<"$group")
  cpu_quantity=$((cpu_quantity * count))
  cpu_total=$((cpu_total + cpu_quantity))

  gpu_quantity=$(jq '.gpu.units' <<<"$group")
  gpu_quantity=$((gpu_quantity * count))
  gpu_total=$((gpu_total + gpu_quantity))

  for storage in $(jq -c '.storage[]' <<<"$group"); do
      storage_size=$(jq -r '.size' <<<"$storage")
      # jq has to be with -r to not quote value
      class=$(jq -r '.class' <<<"$storage")

      if [ -v 'STORAGE_UACT_SCALE[class]' ]; then
        echo "requests unsupported storage class \"$class\"" >&2
        exit 1
      fi

      storage_size=$((storage_size * count))
      storage_cost_uact=$(bc -l <<<"(${storage_size}*${STORAGE_UACT_SCALE[$class]}) + ${storage_cost_uact}")
  done

  endpoint_quantity=$(jq ".endpoint_quantity" <<<"$group")
  endpoint_quantity=$((endpoint_quantity * count))
  endpoint_total=$((endpoint_total + endpoint_quantity))
done

# calculate the total cost in USD for each resource
cpu_cost_uact=$(bc -l <<<"${cpu_total}*${CPU_UACT_SCALE}")
gpu_cost_uact=$(bc -l <<<"${gpu_total}*${GPU_UACT_SCALE}")
memory_cost_uact=$(bc -l <<<"${memory_total}*${MEMORY_UACT_SCALE}")
endpoint_cost_uact=$(bc -l <<<"${endpoint_total}*${ENDPOINT_UACT_SCALE}")

# validate the USD cost for each resource
if [ 1 -eq "$(bc <<<"${cpu_cost_uact}<0")" ] || [ 0 -eq "$(bc <<<"${cpu_cost_uact}<=${MAX_INT64}")" ] ||
  [ 1 -eq "$(bc <<<"${gpu_cost_uact}<0")" ] || [ 0 -eq "$(bc <<<"${gpu_cost_uact}<=${MAX_INT64}")" ] ||
  [ 1 -eq "$(bc <<<"${memory_cost_uact}<0")" ] || [ 0 -eq "$(bc <<<"${memory_cost_uact}<=${MAX_INT64}")" ] ||
  [ 1 -eq "$(bc <<<"${storage_cost_uact}<0")" ] || [ 0 -eq "$(bc <<<"${storage_cost_uact}<=${MAX_INT64}")" ] ||
  [ 1 -eq "$(bc <<<"${endpoint_cost_uact}<0")" ] || [ 0 -eq "$(bc <<<"${endpoint_cost_uact}<=${MAX_INT64}")" ]; then
  echo "invalid cost results for units" >&2
  exit 1
fi

# finally, calculate the total cost in uACT of all resources and validate it
total_cost_uact=$(bc -l <<<"${cpu_cost_uact}+${gpu_cost_uact}+${memory_cost_uact}+${storage_cost_uact}+${endpoint_cost_uact}")
if [ 1 -eq "$(bc <<<"${total_cost_uact}<0")" ] || [ 0 -eq "$(bc <<<"${total_cost_uact}<=${MAX_INT64}")" ]; then
  echo "invalid total cost $total_cost_uact" >&2
  exit 1
fi

# DO NOT INCREASE PRECISION below, it gives varying results during tests on different hosts
printf "%.*f" "$precision" "$total_cost_uact"
