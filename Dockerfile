FROM ubuntu:noble
LABEL "org.opencontainers.image.source"="https://github.com/akash-network/provider"

COPY provider-services /usr/bin/

ENV DEBIAN_FRONTEND=noninteractive

RUN \
    apt-get update \
 && apt-get install -y --no-install-recommends \
    tini \
    jq \
    bc \
    netcat-traditional \
    mawk \
    curl \
    ca-certificates \
    pci.ids \
 && rm -rf /var/lib/apt/lists/*

ENV DEBIAN_FRONTEND=""

# grpcurl is used to probe the inventory operator's gRPC API (e.g. from a
# liveness probe). It is not packaged in apt, so install a pinned release.
# The version and per-arch checksums are supplied as build args (sourced from
# .env via the goreleaser build), so bumping the release does not require
# editing this Dockerfile.
ARG GRPCURL_VERSION
ARG GRPCURL_SHA256_AMD64
ARG GRPCURL_SHA256_ARM64
RUN set -eux; \
    case "$(dpkg --print-architecture)" in \
      amd64) arch=x86_64; sha="${GRPCURL_SHA256_AMD64}" ;; \
      arm64) arch=arm64;  sha="${GRPCURL_SHA256_ARM64}" ;; \
      *) echo "unsupported architecture: $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac; \
    curl -fL --retry 5 --retry-delay 2 --retry-connrefused --connect-timeout 10 --max-time 120 \
      -o /tmp/grpcurl.tar.gz \
      "https://github.com/fullstorydev/grpcurl/releases/download/v${GRPCURL_VERSION}/grpcurl_${GRPCURL_VERSION}_linux_${arch}.tar.gz"; \
    echo "${sha}  /tmp/grpcurl.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/grpcurl.tar.gz -C /usr/bin grpcurl; \
    rm /tmp/grpcurl.tar.gz; \
    grpcurl -version

# default port for provider API
EXPOSE 8443

# default for inventory operator API
EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["provider-services", "--help"]
