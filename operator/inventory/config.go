package inventory

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

var (
	errConfigInvalidVersion = errors.New("invalid version")
)

type ExcludeRules []*regexp.Regexp

type ConfigStorage struct {
	Exclude ExcludeRules `json:"exclude" yaml:"exclude"`
}

type ConfigNodes struct {
	Exclude ExcludeRules `json:"exclude" yaml:"exclude"`
}

type ExcludeNodeStorage struct {
	NodeFilter *regexp.Regexp `json:"node_filter" yaml:"node_filter"`
	Classes    []string       `json:"classes" yaml:"classes"`
}

type ExcludeNodesStorage []ExcludeNodeStorage

type Exclude struct {
	Nodes       ExcludeRules        `json:"nodes" yaml:"nodes"`
	NodeStorage ExcludeNodesStorage `json:"node_storage" yaml:"node_storage"`
}

type Config struct {
	Version        semver.Version `json:"version" yaml:"version"`
	ClusterStorage []string       `json:"cluster_storage" yaml:"cluster_storage"`
	Exclude        Exclude        `json:"exclude" yaml:"exclude"`

	dirty bool
}

var defaultConfig = []byte(`---
version: v1
cluster_storage: []
exclude:
  nodes: []
  node_storage: []
`)

func loadConfig(cfg string, defaultOnErr bool) (res Config, err error) {
	var data []byte

	defer func() {
		if err != nil && defaultOnErr {
			_ = yaml.Unmarshal(defaultConfig, &res)
		}
	}()

	// nolint: gocritic
	if cfg == "" {
		data = defaultConfig
	} else if strings.HasSuffix(cfg, "yaml") {
		data, err = os.ReadFile(viper.GetString(FlagConfig))
	} else {
		data = []byte(cfg)
	}

	if err != nil {
		return res, err
	}

	if err := yaml.Unmarshal(data, &res); err != nil {
		return res, err
	}

	return res, nil
}

func (cfg *Config) FilterOutStorageClasses(available []string) {
	if slices.Equal(cfg.ClusterStorage, available) {
		return
	}

	presentSc := make(map[string]bool)
	requestedSc := make(map[string]bool)

	for _, sc := range available {
		presentSc[sc] = true
	}

	for _, sc := range cfg.ClusterStorage {
		requestedSc[sc] = true
	}

	for sc := range requestedSc {
		if _, exists := presentSc[sc]; !exists {
			delete(requestedSc, sc)
		}
	}

	cfg.ClusterStorage = make([]string, 0, len(requestedSc))

	for sc := range requestedSc {
		cfg.ClusterStorage = append(cfg.ClusterStorage, sc)
	}

	sort.Strings(cfg.ClusterStorage)

	for i := range cfg.Exclude.NodeStorage {
		for j, class := range cfg.Exclude.NodeStorage[i].Classes {
			if _, exists := requestedSc[class]; !exists {
				cfg.Exclude.NodeStorage[i].Classes = remove(cfg.Exclude.NodeStorage[i].Classes, j)
			}
		}
	}

	cfg.dirty = true
}

func (cfg *Config) Copy() Config {
	res := Config{
		ClusterStorage: make([]string, len(cfg.ClusterStorage)),
		Exclude:        cfg.Exclude.Copy(),
		dirty:          false,
	}

	copy(res.ClusterStorage, cfg.ClusterStorage)

	return res
}

func (nd *Exclude) Copy() Exclude {
	res := Exclude{
		Nodes:       nd.Nodes.Copy(),
		NodeStorage: nd.NodeStorage.Copy(),
	}

	return res
}

func (nd *ExcludeRules) Copy() ExcludeRules {
	res := make(ExcludeRules, len(*nd))

	copy(res, *nd)

	return res
}

func (nd *ExcludeNodesStorage) Copy() ExcludeNodesStorage {
	res := make(ExcludeNodesStorage, 0, len(*nd))

	for _, rule := range *nd {
		res = append(res, rule.Copy())
	}

	return res
}

func (nd *ExcludeNodeStorage) Copy() ExcludeNodeStorage {
	res := ExcludeNodeStorage{
		NodeFilter: nd.NodeFilter,
		Classes:    make([]string, len(nd.Classes)),
	}

	copy(res.Classes, nd.Classes)

	return res
}

func (cfg *Config) UnmarshalYAML(node *yaml.Node) error {
	res := Config{}

	var err error

loop:
	for i := 0; i < len(node.Content); i += 2 {
		var val interface{}
		switch node.Content[i].Value {
		case "version":
			if res.Version, err = semver.ParseTolerant(node.Content[i+1].Value); err != nil {
				return fmt.Errorf("%w: %w", errConfigInvalidVersion, err)
			}
			continue loop
		case "cluster_storage":
			val = &res.ClusterStorage
		case "exclude":
			val = &res.Exclude
		default:
			return fmt.Errorf("config: unexpected field %s", node.Content[i].Value) // nolint: err113
		}

		if err := node.Content[i+1].Decode(val); err != nil {
			return err
		}
	}

	availSc := make(map[string]bool)
	for _, sc := range res.ClusterStorage {
		availSc[sc] = true
	}

	for _, exl := range res.Exclude.NodeStorage {
		for _, sc := range exl.Classes {
			if _, exists := availSc[sc]; !exists {
				return fmt.Errorf("storage class \"%s\" from exclude references non-existing class", sc) // nolint: err113
			}
		}

		sort.Strings(exl.Classes)
	}

	sort.Strings(res.ClusterStorage)

	*cfg = res

	return nil
}

func (nd *ExcludeRules) UnmarshalYAML(node *yaml.Node) error {
	var excludes []string

	if err := node.Decode(&excludes); err != nil {
		return err
	}

	res := make(ExcludeRules, 0, len(excludes))
	for i, ex := range excludes {
		if ex == "" {
			return fmt.Errorf("empty regexp filters are not allowed") // nolint: err113
		}

		r, err := regexp.Compile(ex)
		if err != nil {
			return fmt.Errorf("%w: unable to compile exclude \"%s\" at index \"%d\" into regexp", err, ex, i)
		}

		res = append(res, r)
	}

	*nd = res

	return nil
}

func (nd *ExcludeNodeStorage) UnmarshalYAML(node *yaml.Node) error {
	tmp := struct {
		NodeFilter string   `yaml:"node_filter"`
		Classes    []string `yaml:"classes"`
	}{}

	if err := node.Decode(&tmp); err != nil {
		return err
	}

	if tmp.NodeFilter == "" {
		return fmt.Errorf("empty regexp filters are not allowed") // nolint: err113
	}

	r, err := regexp.Compile(tmp.NodeFilter)
	if err != nil {
		return fmt.Errorf("%w: unable to compile exclude node filter\"%s\" into regexp", err, tmp.NodeFilter)
	}

	res := ExcludeNodeStorage{
		NodeFilter: r,
		Classes:    tmp.Classes,
	}

	*nd = res

	return nil
}

func (nd *Exclude) IsNodeExcluded(name string) bool {
	for _, r := range nd.Nodes {
		if r.MatchString(name) {
			return true
		}
	}

	return false
}

func (nd *Exclude) IsStorageNodeExcluded(name string, class string) bool {
	for _, r := range nd.NodeStorage {
		for _, c := range r.Classes {
			if c == class && r.NodeFilter.MatchString(name) {
				return true
			}
		}
	}

	return false
}

func (cfg *Config) HasStorageClass(name string) bool {
	return slices.Contains(cfg.ClusterStorage, name)
}

func (cfg *Config) StorageClassesForNode(name string) []string {
	sc := make([]string, len(cfg.ClusterStorage))
	copy(sc, cfg.ClusterStorage)

	for _, rule := range cfg.Exclude.NodeStorage {
		if !rule.NodeFilter.MatchString(name) {
			continue
		}

		for _, eClass := range rule.Classes {
			for i, class := range sc {
				if eClass == class {
					sc = remove(sc, i)
				}
			}
		}
	}

	res := make([]string, len(sc))
	copy(res, sc)

	return sc
}
