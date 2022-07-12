FROM debian:bullseye
LABEL "org.opencontainers.image.source"="https://github.com/ovrclk/provider-services"

COPY provider-services /bin/

RUN \
    apt-get update \
 && apt-get install -y --no-install-recommends \
    tini \
 && rm -rf /var/lib/apt/lists/*

# default port for provider API
EXPOSE 8443

# default for inventory operator API
EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["provider-services", "--help"]
