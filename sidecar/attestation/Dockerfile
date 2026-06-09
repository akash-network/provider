# syntax=docker/dockerfile:1

# Stage 1: compile the NVML attestation helper against Ubuntu 22.04 glibc
# for compatibility with Kata guest rootfs images.
# This small C program dlopen's libnvidia-ml.so.1 at runtime.
# It runs inside a chroot of the Kata guest rootfs so it picks up
# the driver-matched NVML library.
FROM ubuntu:22.04 AS nvml-build
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*
COPY nvml-helper/nvml_attestation.c /src/nvml_attestation.c
RUN gcc -O2 -o /nvml_attestation /src/nvml_attestation.c -ldl

# Stage 2: final image.
FROM gcr.io/distroless/base:latest
COPY --from=nvml-build /nvml_attestation /usr/bin/nvml_attestation
COPY attestation-sidecar /attestation-sidecar
ENTRYPOINT ["/attestation-sidecar"]
