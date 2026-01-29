package kube

import (
	"math"

	sdkmath "cosmossdk.io/math"
	"k8s.io/apimachinery/pkg/api/resource"

	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
)

type resourcePair struct {
	allocatable resource.Quantity
	allocated   resource.Quantity
}

// type clusterStorage map[string]*resourcePair
//
// func (cs clusterStorage) dup() clusterStorage {
// 	res := make(clusterStorage)
// 	for class, resources := range cs {
// 		res[class] = resources.dup()
// 	}
//
// 	return res
// }
//
// func newResourcePair(allocatable, allocated resource.Quantity) resourcePair {
// 	rp := resourcePair{
// 		allocatable: allocatable,
// 		allocated:   allocated,
// 	}
//
// 	return rp
// }
//
// func rpNewFromAkash(res crd.ResourcePair) *resourcePair {
// 	return &resourcePair{
// 		allocatable: *resource.NewQuantity(int64(res.Allocatable), resource.DecimalSI),
// 		allocated:   *resource.NewQuantity(int64(res.Allocated), resource.DecimalSI),
// 	}
// }
//
// func (rp *resourcePair) dup() *resourcePair {
// 	return &resourcePair{
// 		allocatable: rp.allocatable.DeepCopy(),
// 		allocated:   rp.allocated.DeepCopy(),
// 	}
// }

func (rp *resourcePair) subMilliNLZ(val rtypes.ResourceValue) bool {
	avail := rp.available()

	res := sdkmath.NewInt(avail.MilliValue())
	res = res.Sub(val.Val)
	if res.IsNegative() {
		return false
	}

	allocated := rp.allocated.DeepCopy()
	allocated.Add(*resource.NewMilliQuantity(int64(val.Value()), resource.DecimalSI)) // nolint: gosec

	*rp = resourcePair{
		allocatable: rp.allocatable.DeepCopy(),
		allocated:   allocated,
	}

	return true
}

func (rp *resourcePair) subNLZ(val rtypes.ResourceValue) bool {
	avail := rp.available()

	res := sdkmath.NewInt(avail.Value())
	res = res.Sub(val.Val)

	if res.IsNegative() {
		return false
	}

	allocated := rp.allocated.DeepCopy()
	allocated.Add(*resource.NewQuantity(int64(val.Value()), resource.DecimalSI)) // nolint: gosec

	*rp = resourcePair{
		allocatable: rp.allocatable.DeepCopy(),
		allocated:   allocated,
	}

	return true
}

func (rp *resourcePair) available() resource.Quantity {
	result := rp.allocatable.DeepCopy()

	if result.Value() == -1 {
		result = *resource.NewQuantity(math.MaxInt64, resource.DecimalSI)
	}

	// Modifies the value in place
	(&result).Sub(rp.allocated)
	return result
}
