package bidengine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/shopspring/decimal"

	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/node/types/unit"
	"pkg.akt.dev/go/sdl"

	"github.com/akash-network/provider/cluster/util"
)

type Request struct {
	Owner              string `json:"owner"`
	GSpec              *dtypes.GroupSpec
	AllocatedResources dtypes.ResourceUnits
	PricePrecision     int
}

const (
	DefaultPricePrecision = 6
)

type BidPricingStrategy interface {
	CalculatePrice(ctx context.Context, req Request) (sdk.DecCoin, error)
}

var (
	errAllScalesZero               = errors.New("at least one bid price must be a non-zero number")
	errNoPriceScaleForStorageClass = errors.New("no pricing configured for storage class")
	errScaleNegative               = errors.New("scale price cannot be negative")
)

type Storage map[string]decimal.Decimal

func (ss Storage) IsAnyZero() bool {
	if len(ss) == 0 {
		return true
	}

	for _, val := range ss {
		if val.IsZero() {
			return true
		}
	}

	return false
}

func (ss Storage) IsAnyNegative() bool {
	for _, val := range ss {
		if val.IsNegative() {
			return true
		}
	}

	return false
}

// AllLessThenOrEqual check all storage classes fit into max limits
// note better have dedicated limits for each class
func (ss Storage) AllLessThenOrEqual(val decimal.Decimal) bool {
	for _, storage := range ss {
		if !storage.LessThanOrEqual(val) {
			return false
		}
	}

	return true
}

type scalePricing struct {
	cpuScale      decimal.Decimal
	memoryScale   decimal.Decimal
	storageScale  Storage
	endpointScale decimal.Decimal
	ipScale       decimal.Decimal
}

func MakeScalePricing(
	cpuScale decimal.Decimal,
	memoryScale decimal.Decimal,
	storageScale Storage,
	endpointScale decimal.Decimal,
	ipScale decimal.Decimal,
) (BidPricingStrategy, error) {
	if cpuScale.IsZero() && memoryScale.IsZero() && storageScale.IsAnyZero() && endpointScale.IsZero() && ipScale.IsZero() {
		return nil, errAllScalesZero
	}

	if cpuScale.IsNegative() || memoryScale.IsNegative() || storageScale.IsAnyNegative() || endpointScale.IsNegative() ||
		ipScale.IsNegative() {
		return nil, errScaleNegative
	}

	result := scalePricing{
		cpuScale:      cpuScale,
		memoryScale:   memoryScale,
		storageScale:  storageScale,
		endpointScale: endpointScale,
		ipScale:       ipScale,
	}

	return result, nil
}

var (
	ErrBidQuantityInvalid = errors.New("a bid quantity is invalid")
	ErrBidZero            = errors.New("a bid of zero was produced")
)

func ceilBigRatToBigInt(v *big.Rat) *big.Int {
	numerator := v.Num()
	denom := v.Denom()

	result := big.NewInt(0).Div(numerator, denom)
	if !v.IsInt() {
		result.Add(result, big.NewInt(1))
	}

	return result
}

func (fp scalePricing) CalculatePrice(_ context.Context, req Request) (sdk.DecCoin, error) {
	// Use unlimited precision math here.
	// Otherwise, a correctly crafted order could create a cost of '1' given
	// a possible configuration
	cpuTotal := decimal.NewFromInt(0)
	memoryTotal := decimal.NewFromInt(0)
	storageTotal := make(Storage)
	denom := req.GSpec.Price().Denom

	for k := range fp.storageScale {
		storageTotal[k] = decimal.NewFromInt(0)
	}

	endpointTotal := decimal.NewFromInt(0)
	ipTotal := decimal.NewFromInt(0).Add(fp.ipScale)
	ipTotal = ipTotal.Mul(decimal.NewFromInt(int64(util.GetEndpointQuantityOfResourceGroup(req.GSpec, rtypes.Endpoint_LEASED_IP)))) // nolint: gosec

	// iterate over everything & sum it up
	for _, group := range req.GSpec.Resources {
		groupCount := decimal.NewFromInt(int64(group.Count)) // // nolint: gosec

		cpuQuantity := decimal.NewFromBigInt(group.CPU.Units.Val.BigInt(), 0)
		cpuQuantity = cpuQuantity.Mul(groupCount)
		cpuTotal = cpuTotal.Add(cpuQuantity)

		memoryQuantity := decimal.NewFromBigInt(group.Memory.Quantity.Val.BigInt(), 0)
		memoryQuantity = memoryQuantity.Mul(groupCount)
		memoryTotal = memoryTotal.Add(memoryQuantity)

		for _, storage := range group.Storage {
			storageQuantity := decimal.NewFromBigInt(storage.Quantity.Val.BigInt(), 0)
			storageQuantity = storageQuantity.Mul(groupCount)

			storageClass := sdl.StorageEphemeral
			attr := storage.Attributes.Find(sdl.StorageAttributePersistent)
			if isPersistent, _ := attr.AsBool(); isPersistent {
				attr = storage.Attributes.Find(sdl.StorageAttributeClass)
				if class, set := attr.AsString(); set {
					storageClass = class
				}
			}

			total, exists := storageTotal[storageClass]

			if !exists {
				return sdk.DecCoin{}, fmt.Errorf("%w: %s", errNoPriceScaleForStorageClass, storageClass)
			}

			total = total.Add(storageQuantity)

			storageTotal[storageClass] = total
		}

		endpointQuantity := decimal.NewFromInt(int64(len(group.Endpoints)))
		endpointTotal = endpointTotal.Add(endpointQuantity)
	}

	cpuTotal = cpuTotal.Mul(fp.cpuScale)

	mebibytes := decimal.NewFromInt(unit.Mi)

	memoryTotal = memoryTotal.Div(mebibytes)
	memoryTotal = memoryTotal.Mul(fp.memoryScale)

	for class, total := range storageTotal {
		total = total.Div(mebibytes)

		// at this point presence of class in storageScale has been validated
		total = total.Mul(fp.storageScale[class])

		storageTotal[class] = total
	}

	endpointTotal = endpointTotal.Mul(fp.endpointScale)

	// Each quantity must be non-negative
	// and fit into an Int64
	if cpuTotal.IsNegative() ||
		memoryTotal.IsNegative() ||
		storageTotal.IsAnyNegative() ||
		endpointTotal.IsNegative() ||
		ipTotal.IsNegative() {
		return sdk.DecCoin{}, ErrBidQuantityInvalid
	}

	totalCost := cpuTotal
	totalCost = totalCost.Add(memoryTotal)
	for _, total := range storageTotal {
		totalCost = totalCost.Add(total)
	}
	totalCost = totalCost.Add(endpointTotal)
	totalCost = totalCost.Add(ipTotal)

	if totalCost.IsNegative() {
		return sdk.DecCoin{}, ErrBidQuantityInvalid
	}

	if totalCost.IsZero() {
		// Return an error indicating we can't bid with a cost of zero
		return sdk.DecCoin{}, ErrBidZero
	}

	costDec, err := sdkmath.LegacyNewDecFromStr(totalCost.String())
	if err != nil {
		return sdk.DecCoin{}, err
	}

	if !costDec.LTE(sdkmath.LegacyMaxSortableDec) {
		return sdk.DecCoin{}, ErrBidQuantityInvalid
	}

	return sdk.NewDecCoinFromDec(denom, costDec), nil
}

type randomRangePricing int

func MakeRandomRangePricing() (BidPricingStrategy, error) {
	return randomRangePricing(0), nil
}

func (randomRangePricing) CalculatePrice(_ context.Context, req Request) (sdk.DecCoin, error) {
	minPrice, maxPrice := calculatePriceRange(req.GSpec)
	if minPrice.IsEqual(maxPrice) {
		return maxPrice, nil
	}

	const scale = 10000

	delta := maxPrice.Amount.Sub(minPrice.Amount).Mul(sdkmath.LegacyNewDec(scale))

	minbid := delta.TruncateInt64()
	if minbid < 1 {
		minbid = 1
	}
	val, err := rand.Int(rand.Reader, big.NewInt(minbid))
	if err != nil {
		return sdk.DecCoin{}, err
	}

	scaledValue := sdkmath.LegacyNewDecFromBigInt(val).QuoInt64(scale).QuoInt64(100)
	amount := minPrice.Amount.Add(scaledValue)
	return sdk.NewDecCoinFromDec(minPrice.Denom, amount), nil
}

func calculatePriceRange(gspec *dtypes.GroupSpec) (sdk.DecCoin, sdk.DecCoin) {
	// memory-based pricing:
	//   min: requested memory * configured min price per Gi
	//   max: requested memory * configured max price per Gi

	// assumption: group.Count > 0
	// assumption: all same denom (returned by gspec.Price())
	// assumption: gspec.Price() > 0

	mem := sdkmath.NewInt(0)

	for _, group := range gspec.Resources {
		mem = mem.Add(
			sdkmath.NewIntFromUint64(group.Resources.Memory.Quantity.Value()).
				MulRaw(int64(group.Count)))
	}

	rmax := gspec.Price()

	const minGroupMemPrice = int64(50)
	const maxGroupMemPrice = int64(1048576)

	cmin := sdkmath.LegacyNewDecFromInt(mem.MulRaw(
		minGroupMemPrice).
		Quo(sdkmath.NewInt(unit.Gi)))

	cmax := sdkmath.LegacyNewDecFromInt(mem.MulRaw(
		maxGroupMemPrice).
		Quo(sdkmath.NewInt(unit.Gi)))

	if cmax.GT(rmax.Amount) {
		cmax = rmax.Amount
	}

	if cmin.IsZero() {
		cmin = sdkmath.LegacyNewDec(1)
	}

	if cmax.IsZero() {
		cmax = sdkmath.LegacyNewDec(1)
	}

	return sdk.NewDecCoinFromDec(rmax.Denom, cmin), sdk.NewDecCoinFromDec(rmax.Denom, cmax)
}

var (
	errPathEmpty               = errors.New("script path cannot be the empty string")
	errProcessLimitZero        = errors.New("process limit must be greater than zero")
	errProcessRuntimeLimitZero = errors.New("process runtime limit must be greater than zero")
)

type storageElement struct {
	Class string `json:"class"`
	Size  uint64 `json:"size"`
}

type gpuVendorAttributes struct {
	Model     string  `json:"model"`
	RAM       *string `json:"ram,omitempty"`
	Interface *string `json:"interface,omitempty"`
}

type gpuAttributes struct {
	Vendor map[string]gpuVendorAttributes `json:"vendor,omitempty"`
}

type gpuElement struct {
	Units      uint64        `json:"units"`
	Attributes gpuAttributes `json:"attributes"`
}

type dataForScriptElement struct {
	Memory           uint64           `json:"memory"`
	CPU              uint64           `json:"cpu"`
	GPU              gpuElement       `json:"gpu"`
	Storage          []storageElement `json:"storage"`
	Count            uint32           `json:"count"`
	EndpointQuantity int              `json:"endpoint_quantity"`
	IPLeaseQuantity  uint             `json:"ip_lease_quantity"`
}

type dataForScript struct {
	Resources      []dataForScriptElement `json:"resources"`
	Price          sdk.DecCoin            `json:"price"`
	PricePrecision *int                   `json:"price_precision,omitempty"`
}
