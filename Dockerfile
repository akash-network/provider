FROM debian:bullseye
LABEL "org.opencontainers.image.source"="https://github.com/akash-network/provider"

COPY provider-services /usr/bin/

ENV DEBIAN_FRONTEND=noninteractive

RUN \
    apt-get update \
 && apt-get install -y --no-install-recommends \
    tini \
    ca-certificates \
    pci.ids \
 && rm -rf /var/lib/apt/lists/*

ENV DEBIAN_FRONTEND=

# default port for provider API
EXPOSE 8443

# default for inventory operator API
EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["provider-services", "--help"]
