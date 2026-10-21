// Package shard splits test suites into balanced shards, so CI can run each
// shard on its own machine in parallel.
package shard

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"
)

const (
	// EnvIndex names the variable holding the 1-based shard to run.
	EnvIndex = "E2E_SHARD_INDEX"
	// EnvTotal names the variable holding the number of shards.
	EnvTotal = "E2E_SHARD_TOTAL"
)

// Spec selects shard Index (1-based) out of Total.
type Spec struct {
	Index int
	Total int
}

// FromEnv reads the shard from E2E_SHARD_INDEX and E2E_SHARD_TOTAL. With both
// unset it returns shard 1 of 1, which runs everything. Setting only one of
// them, or a value that does not name a shard, is an error.
func FromEnv() (Spec, error) {
	return fromLookup(os.LookupEnv)
}

func fromLookup(lookup func(string) (string, bool)) (Spec, error) {
	rawIndex, hasIndex := lookup(EnvIndex)
	rawTotal, hasTotal := lookup(EnvTotal)

	switch {
	case !hasIndex && !hasTotal:
		return Spec{Index: 1, Total: 1}, nil
	case !hasIndex:
		return Spec{}, fmt.Errorf("%s is set but %s is not: set both or neither", EnvTotal, EnvIndex)
	case !hasTotal:
		return Spec{}, fmt.Errorf("%s is set but %s is not: set both or neither", EnvIndex, EnvTotal)
	}

	index, err := strconv.Atoi(rawIndex)
	if err != nil {
		return Spec{}, fmt.Errorf("%s=%q is not an integer", EnvIndex, rawIndex)
	}

	total, err := strconv.Atoi(rawTotal)
	if err != nil {
		return Spec{}, fmt.Errorf("%s=%q is not an integer", EnvTotal, rawTotal)
	}

	if total < 1 {
		return Spec{}, fmt.Errorf("%s=%d must be at least 1", EnvTotal, total)
	}

	if index < 1 || index > total {
		return Spec{}, fmt.Errorf("%s=%d must be between 1 and %s=%d", EnvIndex, index, EnvTotal, total)
	}

	return Spec{Index: index, Total: total}, nil
}

// Option configures Assign.
type Option func(*config)

type reservation struct {
	shard int
	d     time.Duration
}

type config struct {
	reservations []reservation
}

// Reserve counts d as work already planned on the 1-based shard, for work that
// runs there besides the assigned items. Reservations on one shard add up.
func Reserve(shard int, d time.Duration) Option {
	return func(c *config) {
		c.reservations = append(c.reservations, reservation{shard: shard, d: d})
	}
}

// Assign splits items into total shards by their estimated durations and
// returns, per shard, the indexes of its items in ascending order, so items
// keep their relative order. Items are placed longest first, each on the shard
// with the least estimated work so far (ties go to the lowest shard), so a
// shard that gets items ends at most one item above the average. The result
// depends only on the inputs. Every estimate must be positive.
func Assign(estimates []time.Duration, total int, opts ...Option) ([][]int, error) {
	if total < 1 {
		return nil, fmt.Errorf("shard count %d must be at least 1", total)
	}

	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	load := make([]time.Duration, total)
	for _, r := range cfg.reservations {
		if r.shard < 1 || r.shard > total {
			return nil, fmt.Errorf("reservation on shard %d: shard must be between 1 and %d", r.shard, total)
		}

		if r.d < 0 {
			return nil, fmt.Errorf("reservation of %s on shard %d must not be negative", r.d, r.shard)
		}

		load[r.shard-1] += r.d
	}

	for i, d := range estimates {
		if d <= 0 {
			return nil, fmt.Errorf("item %d: estimate %s must be positive", i, d)
		}
	}

	// longest first; the stable sort keeps equal estimates in item order
	order := make([]int, len(estimates))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(estimates[b], estimates[a])
	})

	shards := make([][]int, total)
	for i := range shards {
		shards[i] = []int{}
	}

	for _, item := range order {
		target := 0
		for s := 1; s < total; s++ {
			if load[s] < load[target] {
				target = s
			}
		}

		shards[target] = append(shards[target], item)
		load[target] += estimates[item]
	}

	for _, items := range shards {
		slices.Sort(items)
	}

	return shards, nil
}
