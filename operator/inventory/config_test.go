package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfigEmpty(t *testing.T) {
	var data = []byte(`---
version: v1
cluster_storage: []
exclude:
  nodes: []
  node_storage: []
`)

	cfg := &Config{}

	err := yaml.Unmarshal(data, cfg)
	require.NoError(t, err)
	require.Len(t, cfg.ClusterStorage, 0)
	require.Len(t, cfg.Exclude.Nodes, 0)
	require.Len(t, cfg.Exclude.NodeStorage, 0)

	cp := cfg.Copy()
	require.Len(t, cp.ClusterStorage, 0)
	require.Len(t, cp.Exclude.Nodes, 0)
	require.Len(t, cp.Exclude.NodeStorage, 0)
}

func TestConfigClusterStorage(t *testing.T) {
	var data = []byte(`---
version: v1
cluster_storage:
  - default
  - beta2
exclude:
  nodes: []
  node_storage: []
`)

	storage := []string{
		"beta2",
		"default",
	}

	cfg := &Config{}

	err := yaml.Unmarshal(data, cfg)
	require.NoError(t, err)
	require.Len(t, cfg.ClusterStorage, 2)
	require.Equal(t, storage, cfg.ClusterStorage)
	require.Len(t, cfg.Exclude.Nodes, 0)
	require.Len(t, cfg.Exclude.NodeStorage, 0)

	cp := cfg.Copy()
	require.Len(t, cp.ClusterStorage, 2)
	require.Equal(t, storage, cp.ClusterStorage)
	require.Len(t, cp.Exclude.Nodes, 0)
	require.Len(t, cp.Exclude.NodeStorage, 0)

	// there are no exclude rules so any node must have all the same storage classes allowed
	allowedSc := cp.StorageClassesForNode("test")
	require.Len(t, allowedSc, 2)
	require.Equal(t, storage, allowedSc)
}

func TestConfigClusterStorageExclude(t *testing.T) {
	var data = []byte(`---
version: v1
cluster_storage:
  - default
  - beta2
exclude:
  nodes: []
  node_storage:
    - node_filter: ^test
      classes:
        - default
`)

	storage := []string{
		"beta2",
		"default",
	}

	cfg := &Config{}

	err := yaml.Unmarshal(data, cfg)
	require.NoError(t, err)
	require.Len(t, cfg.ClusterStorage, 2)
	require.Equal(t, storage, cfg.ClusterStorage)
	require.Len(t, cfg.Exclude.Nodes, 0)
	require.Len(t, cfg.Exclude.NodeStorage, 1)

	cp := cfg.Copy()
	require.Len(t, cp.ClusterStorage, 2)
	require.Equal(t, storage, cp.ClusterStorage)
	require.Len(t, cp.Exclude.Nodes, 0)
	require.Len(t, cp.Exclude.NodeStorage, 1)

	allowedSc := cp.StorageClassesForNode("test")
	require.Len(t, allowedSc, 1)
	require.Equal(t, storage[0], "beta2")
}

func TestConfigClusterStorageExclude2(t *testing.T) {
	var data = []byte(`---
version: v1
cluster_storage:
  - default
  - beta2
exclude:
  nodes: []
  node_storage:
    - node_filter: ^test
      classes:
        - default
        - beta2
`)

	storage := []string{
		"beta2",
		"default",
	}

	cfg := &Config{}

	err := yaml.Unmarshal(data, cfg)
	require.NoError(t, err)
	require.Len(t, cfg.ClusterStorage, 2)
	require.Equal(t, storage, cfg.ClusterStorage)
	require.Len(t, cfg.Exclude.Nodes, 0)
	require.Len(t, cfg.Exclude.NodeStorage, 1)

	cp := cfg.Copy()
	require.Len(t, cp.ClusterStorage, 2)
	require.Equal(t, storage, cp.ClusterStorage)
	require.Len(t, cp.Exclude.Nodes, 0)
	require.Len(t, cp.Exclude.NodeStorage, 1)

	allowedSc := cp.StorageClassesForNode("test")
	require.Len(t, allowedSc, 0)
}

func TestConfigClusterStorageNotListed(t *testing.T) {
	var data = []byte(`---
version: v1
cluster_storage:
  - default
exclude:
  nodes: []
  node_storage:
    - node_filter: ^test
      classes:
        - default
        - beta2
`)

	cfg := &Config{}

	err := yaml.Unmarshal(data, cfg)
	require.Error(t, err)
}

func TestConfigExcludeNode(t *testing.T) {
	var data = []byte(`---
version: v1
cluster_storage:
  - default
  - beta2
exclude:
  nodes:
    - ^test
`)

	cfg := &Config{}

	err := yaml.Unmarshal(data, cfg)
	require.NoError(t, err)
	require.Len(t, cfg.ClusterStorage, 2)
	require.Len(t, cfg.Exclude.Nodes, 1)
	require.Len(t, cfg.Exclude.NodeStorage, 0)

	cp := cfg.Copy()
	require.Len(t, cp.ClusterStorage, 2)
	require.Len(t, cp.Exclude.Nodes, 1)
	require.Len(t, cp.Exclude.NodeStorage, 0)

	require.True(t, cp.Exclude.IsNodeExcluded("test"))
}

func TestConfigClusterStorageAvailable(t *testing.T) {
	var data = []byte(`---
version: v1
cluster_storage:
  - default
  - beta2
exclude:
  nodes: []
  node_storage:
    - node_filter: ^test
      classes:
        - default
        - beta2
`)

	storage := []string{
		"beta2",
		"default",
	}

	storageC := []string{
		"beta2",
	}

	cfg := &Config{}

	err := yaml.Unmarshal(data, cfg)
	require.NoError(t, err)
	require.Len(t, cfg.ClusterStorage, 2)
	require.Equal(t, storage, cfg.ClusterStorage)
	require.Len(t, cfg.Exclude.Nodes, 0)
	require.Len(t, cfg.Exclude.NodeStorage, 1)

	cp := cfg.Copy()
	cp.FilterOutStorageClasses([]string{"beta2"})

	require.Len(t, cp.ClusterStorage, 1)
	require.Equal(t, storageC, cp.ClusterStorage)
	require.Len(t, cp.Exclude.Nodes, 0)
	require.Len(t, cp.Exclude.NodeStorage, 1)

	allowedSc := cp.StorageClassesForNode("test")
	require.Len(t, allowedSc, 0)
}
