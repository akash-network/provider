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
ENV GRPCURL_VERSION=1.9.3
RUN set -eux; \
    case "$(dpkg --print-architecture)" in \
      amd64) arch=x86_64; sha=a926b62a85787ccf73ef8736b3ae554f1242e39d92bb8767a79d6dd23b11d1d5 ;; \
      arm64) arch=arm64;  sha=b20a00c1cb82ab81ec32696766d4076e99b4cb5ca0823a71767ba64dbea0f263 ;; \
      *) echo "unsupported architecture: $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/grpcurl.tar.gz \
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
