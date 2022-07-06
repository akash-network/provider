FROM debian:bullseye
LABEL "org.opencontainers.image.source"="https://github.com/ovrclk/provider-services"

COPY provider-services /bin/

RUN \
    apt-get update \
 && apt-get install -y --no-install-recommends \
    tini \
 && rm -rf /var/lib/apt/lists/*

EXPOSE 8443

ENTRYPOINT ["/usr/bin/tini", "--", "/bin/provider-services"]
CMD ["--help"]
