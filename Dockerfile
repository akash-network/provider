FROM debian:bullseye
LABEL "org.opencontainers.image.source"="https://github.com/akash-network/provider"

COPY provider-services /usr/bin/

RUN apt -qq update \
 && DEBIAN_FRONTEND=noninteractive apt -qq -y -o Dpkg::Options::="--force-confdef" -o Dpkg::Options::="--force-confold" --no-install-recommends install \
    tini \
    pci.ids \
    curl \
    jq \
    bc \
    mawk \
    ca-certificates \
 && rm -rf /var/lib/apt/lists/*


# default port for provider API
EXPOSE 8443

# default for inventory operator API
EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["provider-services", "--help"]
