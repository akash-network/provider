# syntax=docker/dockerfile:1

# Stage 1: compile the NVML attestation helper against Ubuntu 22.04 glibc
# for compatibility with Kata guest rootfs images.
# This small C program dlopen's libnvidia-ml.so.1 at runtime.
# It runs inside a chroot of the Kata guest rootfs so it picks up
# the driver-matched NVML library.
FROM ubuntu:22.04 AS nvml-build

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
  gcc \
  libc6-dev \
  && rm -rf /var/lib/apt/lists/*
# nvml-helper/ is at different paths depending on build context:
#   - Direct build (make docker): nvml-helper/nvml_attestation.c
#   - GoReleaser (from repo root): sidecar/attestation/nvml-helper/nvml_attestation.c
# ARG lets the caller override the path.
ARG NVML_SRC=nvml-helper/nvml_attestation.c

COPY ${NVML_SRC} /src/nvml_attestation.c

RUN gcc -O2 -o /nvml_attestation /src/nvml_attestation.c -ldl

# Stage 2: final image. Pinned by digest because distroless has no shell to gate
# the ca-certificates version as the provider images do. This digest's bundle
# drops emSign ECC Root CA - G3 and Sectigo AAA Certificate Services (eMudhra
# intermediate key compromise, October 2026) even though its dpkg metadata still
# says 20250419; check the bundle itself before bumping the digest.
FROM gcr.io/distroless/base-debian13@sha256:65be09d063b5d4737995ca5a17bb7e12ed990412751ec8ec212c9d74db257639

COPY --from=nvml-build /nvml_attestation /usr/bin/nvml_attestation
COPY attestation-sidecar /attestation-sidecar

ENTRYPOINT ["/attestation-sidecar"]
