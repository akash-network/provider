package hostprobe

import (
	"sort"
)

func FindDiscrepancies(results []SourceResult) []Discrepancy {
	type propertyKey struct {
		class    string
		property string
	}

	values := make(map[propertyKey]map[string][]string)
	for _, result := range results {
		if result.Status != SourceStatusOK {
			continue
		}

		for property, value := range result.Properties {
			if value == "" {
				continue
			}

			key := propertyKey{
				class:    result.HardwareClass,
				property: property,
			}
			if values[key] == nil {
				values[key] = make(map[string][]string)
			}

			values[key][value] = append(values[key][value], result.Name)
		}
	}

	discrepancies := make([]Discrepancy, 0)
	for key, byValue := range values {
		if len(byValue) < 2 {
			continue
		}

		item := Discrepancy{
			HardwareClass: key.class,
			Property:      key.property,
			Values:        make([]DiscrepancyValue, 0, len(byValue)),
		}

		for value, sources := range byValue {
			sort.Strings(sources)
			item.Values = append(item.Values, DiscrepancyValue{
				Source: joinList(sources),
				Value:  value,
			})
		}

		sort.Slice(item.Values, func(i, j int) bool {
			return item.Values[i].Value < item.Values[j].Value
		})

		discrepancies = append(discrepancies, item)
	}

	sort.Slice(discrepancies, func(i, j int) bool {
		if discrepancies[i].HardwareClass == discrepancies[j].HardwareClass {
			return discrepancies[i].Property < discrepancies[j].Property
		}

		return discrepancies[i].HardwareClass < discrepancies[j].HardwareClass
	})

	return discrepancies
}
