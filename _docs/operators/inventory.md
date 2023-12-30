# Inventory operator

New inventory operator removes need to manually label and annotate node with specific features (e.g. persistent storage or GPUs)

## Services

Inventory consist of two parts:
- inventory service itself which scrapes information about cluster resources
- node scraper: deployed as DaemonSet, and it scrapes hardware specific information about node

Inventory service watches all online node scrapers via service discovery and connects to them using [gRPC endpoint](https://github.com/akash-network/akash-api/blob/40e1584bc52f8753296e07a562265a034bf35bef/proto/provider/akash/inventory/v1/service.proto#L13).
Node scraper then streams changes in node resources as well as hardware snapshot

## Inventory

### Config

Configuration is designed to allow all by default (unless specified otherwise), and strip out only things that specified,
thus by default, inventory operator will label and annotate all healthy nodes to be shown as available to the provider.

#### Config format

Config is a YAML file with following structure
```yaml
version: v1                              # mandatory
cluster_storage:                         # optional. list of storage classes that available as inventory
  - beta2                                # in this example beta2 and default storage classes will be available as inventory, even if cluster has others
  - default                              #
exclude:                                 # optional. exclusion rules 
  nodes:                                 # list of nodes that need to be excluded from the inventory. each entry is treated as RE2 regexp
    - ^test.*                            #   for example: exclude all nodes with name starting as test. rules may overlap. matching will stop on the first match
  node_storage:                          # optional: list of nodes that need to be excluded from providing persistent storage
    - node_filter: ^test2.*              #   mandatory: is treated as RE2 regexp
      classes:                           #   optional: list of storage classes that have to be excluded for all matching nodes
        - default                        #     for example: nodes starting with name test2 will not participate in bids that require default storage class
```

Live changes of the config are allowed without restarting inventory service.

#### Default configuration
```yaml
version: v1
cluster_storage: []
exclude:
  nodes: []
  node_storage: []
``` 

In case when no node restrictions are in plane (or cluster is single node) default configuration can be used. No config needs to be passed to the service,
however, when change to default config is needed - then service has to be restarted.

To prevent that, it is recommended to deploy default config via ConfigMap and mount it to the operator-inventory deployment.
Path to the file can be set by either `AP_CONFIG` environment variable or via `--config` flag.
