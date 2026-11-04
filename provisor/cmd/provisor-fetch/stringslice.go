package main

import "strings"

// stringSliceFlag implements flag.Value for a repeatable string flag that
// starts pre-populated with defaults; the first explicit Set clears those
// defaults rather than appending to them.
type stringSliceFlag struct {
	values   []string
	explicit bool
}

func newStringSliceFlag(defaults ...string) *stringSliceFlag {
	return &stringSliceFlag{values: append([]string(nil), defaults...)}
}

func (s *stringSliceFlag) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(s.values, ",")
}

func (s *stringSliceFlag) Set(v string) error {
	if !s.explicit {
		s.values = nil
		s.explicit = true
	}
	s.values = append(s.values, v)
	return nil
}
