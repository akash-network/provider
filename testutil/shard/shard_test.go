package shard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// secs builds estimates from whole seconds.
func secs(values ...int) []time.Duration {
	out := make([]time.Duration, len(values))
	for i, v := range values {
		out[i] = time.Duration(v) * time.Second
	}

	return out
}

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		val, ok := env[key]
		return val, ok
	}
}

func TestFromLookup(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Spec
		wantErr bool
	}{
		{
			name: "both unset runs everything as one shard",
			env:  map[string]string{},
			want: Spec{Index: 1, Total: 1},
		},
		{
			name: "middle shard",
			env:  map[string]string{"E2E_SHARD_INDEX": "2", "E2E_SHARD_TOTAL": "3"},
			want: Spec{Index: 2, Total: 3},
		},
		{
			name: "last shard",
			env:  map[string]string{"E2E_SHARD_INDEX": "3", "E2E_SHARD_TOTAL": "3"},
			want: Spec{Index: 3, Total: 3},
		},
		{
			name:    "only index set",
			env:     map[string]string{"E2E_SHARD_INDEX": "1"},
			wantErr: true,
		},
		{
			name:    "only total set",
			env:     map[string]string{"E2E_SHARD_TOTAL": "3"},
			wantErr: true,
		},
		{
			name:    "index zero",
			env:     map[string]string{"E2E_SHARD_INDEX": "0", "E2E_SHARD_TOTAL": "3"},
			wantErr: true,
		},
		{
			name:    "negative index",
			env:     map[string]string{"E2E_SHARD_INDEX": "-1", "E2E_SHARD_TOTAL": "3"},
			wantErr: true,
		},
		{
			name:    "index above total",
			env:     map[string]string{"E2E_SHARD_INDEX": "4", "E2E_SHARD_TOTAL": "3"},
			wantErr: true,
		},
		{
			name:    "total zero",
			env:     map[string]string{"E2E_SHARD_INDEX": "1", "E2E_SHARD_TOTAL": "0"},
			wantErr: true,
		},
		{
			name:    "non-numeric index",
			env:     map[string]string{"E2E_SHARD_INDEX": "first", "E2E_SHARD_TOTAL": "3"},
			wantErr: true,
		},
		{
			name:    "empty total",
			env:     map[string]string{"E2E_SHARD_INDEX": "1", "E2E_SHARD_TOTAL": ""},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fromLookup(lookupFrom(tt.env))
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// The workflow sets these variable names, so FromEnv must read exactly them.
func TestFromEnvReadsShardVariables(t *testing.T) {
	t.Setenv("E2E_SHARD_INDEX", "2")
	t.Setenv("E2E_SHARD_TOTAL", "3")

	got, err := FromEnv()
	require.NoError(t, err)
	require.Equal(t, Spec{Index: 2, Total: 3}, got)
}

func TestAssign(t *testing.T) {
	tests := []struct {
		name      string
		estimates []time.Duration
		total     int
		opts      []Option
		want      [][]int
	}{
		{
			// 8 -> s1, 5 -> s2, 4 -> s2, 3 -> s1, 2 -> s2: loads 11 and 11
			name:      "longest first onto the least loaded shard",
			estimates: secs(5, 3, 8, 2, 4),
			total:     2,
			want:      [][]int{{1, 2}, {0, 3, 4}},
		},
		{
			// s1 starts at 6: 8 -> s2, 5 -> s1, 4 -> s2, 3 -> s1, 2 -> s2
			name:      "reserved time counts as load",
			estimates: secs(5, 3, 8, 2, 4),
			total:     2,
			opts:      []Option{Reserve(1, 6*time.Second)},
			want:      [][]int{{0, 1}, {2, 3, 4}},
		},
		{
			name:      "reservations on one shard add up",
			estimates: secs(5, 3, 8, 2, 4),
			total:     2,
			opts:      []Option{Reserve(1, 3*time.Second), Reserve(1, 3*time.Second)},
			want:      [][]int{{0, 1}, {2, 3, 4}},
		},
		{
			name:      "ties go to the lowest shard",
			estimates: secs(1, 1, 1, 1),
			total:     2,
			want:      [][]int{{0, 2}, {1, 3}},
		},
		{
			name:      "items keep their original order within a shard",
			estimates: secs(2, 1, 3),
			total:     1,
			want:      [][]int{{0, 1, 2}},
		},
		{
			name:      "more shards than items leaves empty shards",
			estimates: secs(3, 1),
			total:     4,
			want:      [][]int{{0}, {1}, {}, {}},
		},
		{
			name:      "no items",
			estimates: nil,
			total:     3,
			want:      [][]int{{}, {}, {}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Assign(tt.estimates, tt.total, tt.opts...)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// Every item lands on exactly one shard, in ascending order, and no shard that
// gets items ends more than one item above the average load.
func TestAssignPlacesEveryItemOnceWithinBound(t *testing.T) {
	sets := [][]time.Duration{
		secs(),
		secs(1),
		secs(5, 3, 8, 2, 4),
		secs(7, 7, 7, 7, 7, 7, 7),
		secs(100, 1, 1, 1, 1, 1, 1, 1, 1, 1),
		secs(75, 60, 120, 110, 100, 115, 100, 75, 135, 110, 85),
	}

	for _, estimates := range sets {
		for total := 1; total <= len(estimates)+2; total++ {
			reservations := []map[int]time.Duration{nil, {1: 110 * time.Second}}
			if total >= 2 {
				reservations = append(reservations, map[int]time.Duration{2: 1000 * time.Second})
			}

			for _, reserved := range reservations {
				opts := make([]Option, 0, len(reserved))
				for shard, d := range reserved {
					opts = append(opts, Reserve(shard, d))
				}

				got, err := Assign(estimates, total, opts...)
				require.NoError(t, err)
				require.Len(t, got, total)

				var sum, longest time.Duration
				for _, d := range estimates {
					sum += d
					longest = max(longest, d)
				}
				for _, d := range reserved {
					sum += d
				}

				seen := make([]int, len(estimates))
				for s, items := range got {
					load := reserved[s+1]
					for i, item := range items {
						seen[item]++
						load += estimates[item]
						if i > 0 {
							require.Less(t, items[i-1], item, "shard %d items out of order: %v", s+1, items)
						}
					}

					if len(items) > 0 {
						// load <= sum/total + longest, kept in integers
						require.LessOrEqual(t, int64(load)*int64(total), int64(sum)+int64(total)*int64(longest),
							"shard %d of %d over bound: %v", s+1, total, got)
					}
				}

				for item, n := range seen {
					require.Equal(t, 1, n, "item %d assigned %d times: %v", item, n, got)
				}
			}
		}
	}
}

func TestAssignRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		estimates []time.Duration
		total     int
		opts      []Option
	}{
		{name: "zero shards", estimates: secs(1), total: 0},
		{name: "negative shards", estimates: secs(1), total: -1},
		{name: "zero estimate", estimates: []time.Duration{time.Second, 0}, total: 2},
		{name: "negative estimate", estimates: []time.Duration{-time.Second}, total: 1},
		{name: "reserve on shard zero", estimates: secs(1), total: 2, opts: []Option{Reserve(0, time.Second)}},
		{name: "reserve above total", estimates: secs(1), total: 2, opts: []Option{Reserve(3, time.Second)}},
		{name: "negative reservation", estimates: secs(1), total: 2, opts: []Option{Reserve(1, -time.Second)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Assign(tt.estimates, tt.total, tt.opts...)
			require.Error(t, err)
		})
	}
}
