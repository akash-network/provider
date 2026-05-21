package hostprobe

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"sync"
	"time"

	aepinventory "github.com/akash-network/provider/verification/inventory"
)

const defaultSourceTimeout = 2 * time.Second

type Source interface {
	Name() string
	HardwareClass() string
	Method() string
	TrustDomain() string
	Collect(context.Context, FileSource) (map[string]string, error)
}

type Config struct {
	Root          string
	SourceTimeout time.Duration
	Sources       []Source
	Now           func() time.Time
	Hostname      func() (string, error)
}

type Collector struct {
	root          string
	sourceTimeout time.Duration
	sources       []Source
	now           func() time.Time
	hostname      func() (string, error)
}

func NewCollector(cfg Config) *Collector {
	if cfg.SourceTimeout <= 0 {
		cfg.SourceTimeout = defaultSourceTimeout
	}

	if cfg.Sources == nil {
		cfg.Sources = DefaultSources()
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.Hostname == nil {
		cfg.Hostname = os.Hostname
	}

	if cfg.Root == "" {
		cfg.Root = "/"
	}

	return &Collector{
		root:          cfg.Root,
		sourceTimeout: cfg.SourceTimeout,
		sources:       append([]Source(nil), cfg.Sources...),
		now:           cfg.Now,
		hostname:      cfg.Hostname,
	}
}

func (c *Collector) Name() string {
	return EvidenceSectionName
}

func (c *Collector) Collect(ctx context.Context) (aepinventory.EvidenceSection, error) {
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		return aepinventory.EvidenceSection{}, err
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return aepinventory.EvidenceSection{}, err
	}

	return aepinventory.EvidenceSection{
		Name:    EvidenceSectionName,
		Payload: payload,
	}, nil
}

func (c *Collector) Snapshot(ctx context.Context) (Snapshot, error) {
	start := time.Now()
	fs := NewFileSource(c.root)

	hostname, err := c.hostname()
	if err != nil {
		hostname = ""
	}

	sources := c.collectSources(ctx, fs)

	return Snapshot{
		SchemaVersion: SnapshotSchemaVersion,
		Host: Host{
			Hostname:      hostname,
			OS:            runtime.GOOS,
			Arch:          runtime.GOARCH,
			KernelRelease: kernelRelease(fs),
		},
		CollectedAt:    c.now().UTC(),
		DurationMS:     time.Since(start).Milliseconds(),
		Sources:        sources,
		Discrepancies:  FindDiscrepancies(sources),
		Virtualization: DetectVirtualization(sources),
	}, nil
}

func (c *Collector) collectSources(ctx context.Context, fs FileSource) []SourceResult {
	results := make([]SourceResult, len(c.sources))

	var wg sync.WaitGroup
	for idx, source := range c.sources {
		wg.Add(1)
		go func(idx int, source Source) {
			defer wg.Done()
			results[idx] = c.collectSource(ctx, fs, source)
		}(idx, source)
	}

	wg.Wait()

	return results
}

func (c *Collector) collectSource(ctx context.Context, fs FileSource, source Source) SourceResult {
	start := time.Now()
	res := SourceResult{
		Name:          source.Name(),
		HardwareClass: source.HardwareClass(),
		Method:        source.Method(),
		TrustDomain:   source.TrustDomain(),
		Status:        SourceStatusOK,
	}

	sourceCtx, cancel := context.WithTimeout(ctx, c.sourceTimeout)
	defer cancel()

	type output struct {
		properties map[string]string
		err        error
	}

	ch := make(chan output, 1)
	go func() {
		properties, err := source.Collect(sourceCtx, fs)
		ch <- output{properties: properties, err: err}
	}()

	select {
	case out := <-ch:
		res.DurationMS = time.Since(start).Milliseconds()
		if out.err != nil {
			res.Error = out.err.Error()
			if errors.Is(out.err, context.DeadlineExceeded) {
				res.Status = SourceStatusTimeout
			} else if errors.Is(out.err, ErrUnavailable) {
				res.Status = SourceStatusUnavailable
			} else {
				res.Status = SourceStatusError
			}
			return res
		}

		res.Properties = copyProperties(out.properties)
		return res
	case <-sourceCtx.Done():
		res.DurationMS = time.Since(start).Milliseconds()
		res.Status = SourceStatusTimeout
		res.Error = sourceCtx.Err().Error()
		return res
	}
}

func kernelRelease(fs FileSource) string {
	release, err := fs.ReadString("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}

	return release
}

func copyProperties(properties map[string]string) map[string]string {
	if len(properties) == 0 {
		return nil
	}

	res := make(map[string]string, len(properties))
	for key, value := range properties {
		res[key] = value
	}

	return res
}
