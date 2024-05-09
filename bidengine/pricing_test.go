package bidengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/akash-api/go/node/types/unit"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/akash-network/node/sdl"
	"github.com/akash-network/node/testutil"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/cluster/util"
)

func Test_ScalePricingRejectsAllZero(t *testing.T) {
	pricing, err := MakeScalePricing(decimal.Zero, decimal.Zero, make(Storage), decimal.Zero, decimal.Zero)
	require.NotNil(t, err)
	require.Nil(t, pricing)
}

func Test_ScalePricingAcceptsOneForASingleScale(t *testing.T) {
	pricing, err := MakeScalePricing(decimal.NewFromInt(1), decimal.Zero, make(Storage), decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	pricing, err = MakeScalePricing(decimal.Zero, decimal.NewFromInt(1), make(Storage), decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	storageScale := Storage{
		"": decimal.NewFromInt(1),
	}
	pricing, err = MakeScalePricing(decimal.Zero, decimal.Zero, storageScale, decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	pricing, err = MakeScalePricing(decimal.Zero, decimal.Zero, make(Storage), decimal.NewFromInt(1), decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)
}

func defaultGroupSpecCPUMem() *dtypes.GroupSpec {
	gspec := &dtypes.GroupSpec{
		Name:         "",
		Requirements: atypes.PlacementRequirements{},
		Resources:    make(dtypes.ResourceUnits, 1),
	}

	cpu := atypes.CPU{}
	cpu.Units = atypes.NewResourceValue(11)

	memory := atypes.Memory{}
	memory.Quantity = atypes.NewResourceValue(10000)

	clusterResources := atypes.Resources{
		CPU:    &cpu,
		Memory: &memory,
	}

	price := sdk.NewDecCoin("uakt", sdk.NewInt(23))
	resource := dtypes.ResourceUnit{
		Resources: clusterResources,
		Count:     1,
		Price:     price,
	}

	gspec.Resources[0] = resource
	gspec.Resources[0].Resources.Endpoints = make([]atypes.Endpoint, testutil.RandRangeInt(1, 10))
	return gspec
}

func defaultGroupSpec() *dtypes.GroupSpec {
	gspec := &dtypes.GroupSpec{
		Name:         "",
		Requirements: atypes.PlacementRequirements{},
		Resources:    make(dtypes.ResourceUnits, 1),
	}

	clusterResources := atypes.Resources{
		CPU: &atypes.CPU{
			Units: atypes.NewResourceValue(11),
		},
		Memory: &atypes.Memory{
			Quantity: atypes.NewResourceValue(10000),
		},
		GPU: &atypes.GPU{
			Units: atypes.NewResourceValue(0),
		},
		Storage: atypes.Volumes{
			atypes.Storage{
				Quantity: atypes.NewResourceValue(4096),
			},
		},
	}
	price := sdk.NewDecCoin(testutil.CoinDenom, sdk.NewInt(23))
	resource := dtypes.ResourceUnit{
		Resources: clusterResources,
		Count:     1,
		Price:     price,
	}

	gspec.Resources[0] = resource
	gspec.Resources[0].Resources.Endpoints = make([]atypes.Endpoint, testutil.RandRangeInt(1, 10))
	return gspec
}

func Test_ScalePricingFailsOnOverflow(t *testing.T) {
	storageScale := Storage{
		sdl.StorageEphemeral: decimal.NewFromInt(1),
	}

	pricing, err := MakeScalePricing(decimal.New(math.MaxInt64, 2), decimal.Zero, storageScale, decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	price, err := pricing.CalculatePrice(context.Background(), req, nil)

	require.Equal(t, sdk.DecCoin{}, price)
	require.Equal(t, err, ErrBidQuantityInvalid)
}

func Test_ScalePricingOnCpu(t *testing.T) {
	cpuScale := decimal.NewFromInt(22)

	pricing, err := MakeScalePricing(cpuScale, decimal.Zero, make(Storage), decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	gspec := defaultGroupSpecCPUMem()
	cpuQuantity := uint64(13)
	gspec.Resources[0].Resources.CPU.Units = atypes.NewResourceValue(cpuQuantity)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: gspec,
	}

	price, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	expectedPrice := testutil.AkashDecCoin(t, cpuScale.IntPart()*int64(cpuQuantity))
	require.Equal(t, expectedPrice, price)
}

func Test_ScalePricingOnMemory(t *testing.T) {
	memoryScale := uint64(23)
	memoryPrice := decimal.NewFromInt(int64(memoryScale)).Mul(decimal.NewFromInt(unit.Mi))
	pricing, err := MakeScalePricing(decimal.Zero, memoryPrice, make(Storage), decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	gspec := defaultGroupSpecCPUMem()
	memoryQuantity := uint64(123456)
	gspec.Resources[0].Resources.Memory.Quantity = atypes.NewResourceValue(memoryQuantity)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: gspec,
	}

	price, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)

	expectedPrice := testutil.AkashDecCoin(t, int64(memoryScale*memoryQuantity))
	require.Equal(t, expectedPrice, price)
}

func Test_ScalePricingOnMemoryLessThanOne(t *testing.T) {
	memoryScale := uint64(1) // 1 uakt per megabyte
	memoryPrice := decimal.NewFromInt(int64(memoryScale))
	pricing, err := MakeScalePricing(decimal.Zero, memoryPrice, make(Storage), decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	gspec := defaultGroupSpecCPUMem()
	// Make a resource exactly 1 byte
	memoryQuantity := uint64(1)
	gspec.Resources[0].Resources.Memory.Quantity = atypes.NewResourceValue(memoryQuantity)
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: gspec,
	}
	price, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)

	expectedPrice, err := sdk.NewDecFromStr("0.0000009536743164")
	require.NoError(t, err)
	require.Equal(t, expectedPrice, price.Amount)

	// Make a resource exactly 1 less byte less than two megabytes
	memoryQuantity = uint64(2*unit.Mi - 1)
	gspec.Resources[0].Resources.Memory.Quantity = atypes.NewResourceValue(memoryQuantity)
	price, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)
	require.NotNil(t, price)

	expectedPrice, err = sdk.NewDecFromStr("1.9999990463256836")
	require.NoError(t, err)
	require.Equal(t, expectedPrice, price.Amount)

	require.NoError(t, err)
}

func decNearly(t *testing.T, v sdk.Dec, expected int64) {
	t.Helper()
	delta, err := sdk.NewDecFromStr("0.00001")
	require.NoError(t, err)

	expectedLow := sdk.NewDec(expected).Sub(delta)
	require.True(t, v.GT(expectedLow), "%v should be greater than %v", v.String(), expectedLow.String())

	expectedHigh := sdk.NewDec(expected).Add(delta)
	require.True(t, v.LT(expectedHigh), "%v should be less than %v", v.String(), expectedHigh.String())
}

func Test_ScalePricingOnStorage(t *testing.T) {
	storageScale := uint64(24)
	storagePrice := Storage{
		sdl.StorageEphemeral: decimal.NewFromInt(int64(storageScale)).Mul(decimal.NewFromInt(unit.Mi)),
	}

	pricing, err := MakeScalePricing(decimal.Zero, decimal.Zero, storagePrice, decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	gspec := defaultGroupSpec()
	storageQuantity := uint64(98765)
	gspec.Resources[0].Resources.Storage[0].Quantity = atypes.NewResourceValue(storageQuantity)
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: gspec,
	}
	price, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)

	decNearly(t, price.Amount, int64(storageScale*storageQuantity))
}

func Test_ScalePricingByCountOfResources(t *testing.T) {
	storageScale := uint64(3)
	storagePrice := Storage{
		sdl.StorageEphemeral: decimal.NewFromInt(int64(storageScale)).Mul(decimal.NewFromInt(unit.Mi)),
	}

	pricing, err := MakeScalePricing(decimal.Zero, decimal.Zero, storagePrice, decimal.Zero, decimal.Zero)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	gspec := defaultGroupSpec()
	storageQuantity := uint64(111)
	gspec.Resources[0].Resources.Storage[0].Quantity = atypes.NewResourceValue(storageQuantity)
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: gspec,
	}
	firstPrice, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)

	require.NoError(t, err)
	decNearly(t, firstPrice.Amount, int64(storageScale*storageQuantity))

	gspec.Resources[0].Count = 2
	secondPrice, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)
	decNearly(t, secondPrice.Amount, 2*int64(storageScale*storageQuantity))
}

func Test_ScalePricingForIPs(t *testing.T) {
	ipPriceInt := int64(testutil.RandRangeInt(100, 1000))
	ipPrice := decimal.NewFromInt(ipPriceInt)

	pricing, err := MakeScalePricing(decimal.Zero, decimal.Zero, Storage{
		sdl.StorageEphemeral: decimal.Zero,
	}, decimal.Zero, ipPrice)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	gspec := defaultGroupSpec()
	gspec.Resources[0].Resources.Endpoints = append(gspec.Resources[0].Resources.Endpoints, atypes.Endpoint{
		Kind:           atypes.Endpoint_LEASED_IP,
		SequenceNumber: 1367,
	})

	require.Equal(t, uint(1), util.GetEndpointQuantityOfResourceGroup(gspec, atypes.Endpoint_LEASED_IP))
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: gspec,
	}
	price, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)

	require.NoError(t, err)
	decNearly(t, price.Amount, ipPriceInt)

	gspec.Resources[0].Resources.Endpoints = append(gspec.Resources[0].Resources.Endpoints, atypes.Endpoint{
		Kind:           atypes.Endpoint_LEASED_IP,
		SequenceNumber: 1368,
	})
	require.Equal(t, uint(2), util.GetEndpointQuantityOfResourceGroup(gspec, atypes.Endpoint_LEASED_IP))
	price, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)

	require.NoError(t, err)
	decNearly(t, price.Amount, 2*ipPriceInt)

	gspec.Resources[0].Count = 33 // any number greater than 1 works here
	price, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)
	decNearly(t, price.Amount, 2*ipPriceInt)
}

func Test_ScriptPricingRejectsEmptyStringForPath(t *testing.T) {
	pricing, err := MakeShellScriptPricing("", 1, 30000*time.Millisecond)
	require.NotNil(t, err)
	require.Nil(t, pricing)
	require.Contains(t, err.Error(), "empty string")
}

func Test_ScriptPricingRejectsProcessLimitOfZero(t *testing.T) {
	pricing, err := MakeShellScriptPricing("a", 0, 30000*time.Millisecond)
	require.NotNil(t, err)
	require.Nil(t, pricing)
	require.Contains(t, err.Error(), "process limit")
}

func Test_ScriptPricingRejectsTimeoutOfZero(t *testing.T) {
	pricing, err := MakeShellScriptPricing("a", 1, 0*time.Millisecond)
	require.NotNil(t, err)
	require.Nil(t, pricing)
	require.Contains(t, err.Error(), "runtime limit")
}

func Test_ScriptPricingFailsWhenScriptDoesNotExist(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}
	_, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.IsType(t, &os.PathError{}, errors.Unwrap(err))
}

func Test_ScriptPricingFailsWhenScriptExitsNonZero(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	_, err = fout.WriteString("#!/bin/sh\nexit 1")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	_, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.IsType(t, &exec.ExitError{}, errors.Unwrap(err))
}

func Test_ScriptPricingFailsWhenScriptExitsWithoutWritingResultToStdout(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	_, err = fout.WriteString("#!/bin/sh\nexit 0")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	_, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.ErrorIs(t, err, io.EOF)
}

func Test_ScriptPricingFailsWhenScriptWritesZeroResult(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	_, err = fout.WriteString("#!/bin/sh\necho 0\nexit 0")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	_, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.Equal(t, ErrBidZero, err)
}

func Test_ScriptPricingFailsWhenScriptWritesNegativeResult(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	_, err = fout.WriteString("#!/bin/sh\necho -1\nexit 0")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	_, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.Equal(t, ErrBidQuantityInvalid, err)
}

func Test_ScriptPricingWhenScriptWritesFractionalResult(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	_, err = fout.WriteString("#!/bin/sh\necho 1.5\nexit 0")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	result, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)
	expectedPrice, err := sdk.NewDecFromStr("1.5")
	require.NoError(t, err)
	require.Equal(t, result.Amount, expectedPrice)
}

func Test_ScriptPricingFailsWhenScriptWritesOverflowResult(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	// Write the maximum value, followed by zero, so it is 10x
	_, err = fmt.Fprintf(fout, "#!/bin/sh\necho %s0\nexit 0", sdk.MaxSortableDec.String())
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	_, err = pricing.CalculatePrice(context.Background(), req, nil)
	require.ErrorIs(t, err, ErrBidQuantityInvalid)
}

func Test_ScriptPricingReturnsResultFromScript(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	// Value must not have a decimal
	_, err = fout.WriteString("#!/bin/sh\necho 132\nexit 0")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	price, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)
	require.Equal(t, "uakt", price.Denom)
	require.Equal(t, sdk.NewDec(132), price.Amount)
}

func Test_ScriptPricingDoesNotExhaustSemaphore(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	_, err = fout.WriteString("#!/bin/sh\necho 1\nexit 0")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 10, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	// run the script lots of time to make sure the channel used
	// as a semaphore always has things returned to it
	for i := 0; i != 111; i++ {
		req := Request{
			Owner: testutil.AccAddress(t).String(),
			GSpec: defaultGroupSpec(),
		}

		_, err = pricing.CalculatePrice(context.Background(), req, nil)
		require.NoError(t, err)
	}
}

func Test_ScriptPricingStopsByContext(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	_, err = fout.WriteString("#!/bin/sh\nsleep 4\n")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 10, 5000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}
	_, err = pricing.CalculatePrice(ctx, req, nil)
	require.Error(t, err)
	require.Equal(t, context.Canceled, err)
}

func Test_ScriptPricingStopsByTimeout(t *testing.T) {
	_, err := os.Stat("/bin/bash")
	if os.IsNotExist(err) {
		t.Skip("cannot run without bash shell")
	}
	require.NoError(t, err)
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	_, err = fout.WriteString("#!/bin/bash\nsleep 10\n")
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 10, 1*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	ctx := context.Background()
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: defaultGroupSpec(),
	}

	_, err = pricing.CalculatePrice(ctx, req, nil)
	require.Error(t, err)
	require.Equal(t, context.DeadlineExceeded, err)
}

func Test_ScriptPricingWritesJsonToStdin(t *testing.T) {
	tempdir := t.TempDir()

	scriptPath := path.Join(tempdir, "test_script.sh")
	jsonPath := path.Join(tempdir, "stdin.json")
	fout, err := os.OpenFile(scriptPath, os.O_WRONLY|os.O_CREATE, os.ModePerm)
	require.NoError(t, err)
	// Use cat to dump stdin into a file
	_, err = fout.WriteString(fmt.Sprintf("#!/bin/sh\ncat > %q\necho 1\nexit 0", jsonPath))
	require.NoError(t, err)
	err = fout.Close()
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	gspec := defaultGroupSpec()
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: gspec,
	}

	price, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)
	require.Equal(t, "uakt", price.Denom)
	require.Equal(t, sdk.NewDec(1), price.Amount)
	// Open the file and make sure it has the JSON
	fin, err := os.Open(jsonPath)
	require.NoError(t, err)
	defer func() {
		_ = fin.Close()
	}()
	decoder := json.NewDecoder(fin)
	data := dataForScript{}
	err = decoder.Decode(&data)
	require.NoError(t, err)

	require.Len(t, data.Resources, len(gspec.Resources))

	for i, r := range gspec.Resources {
		require.Equal(t, r.Resources.CPU.Units.Val.Uint64(), data.Resources[i].CPU)
		require.Equal(t, r.Resources.Memory.Quantity.Val.Uint64(), data.Resources[i].Memory)
		require.Equal(t, r.Resources.Storage[0].Quantity.Val.Uint64(), data.Resources[i].Storage[0].Size)
		require.Equal(t, r.Count, data.Resources[i].Count)
		require.Equal(t, len(r.Resources.Endpoints), data.Resources[i].EndpointQuantity)
		require.Equal(t, util.GetEndpointQuantityOfResourceUnits(r.Resources, atypes.Endpoint_LEASED_IP), data.Resources[i].IPLeaseQuantity)
	}
}

func Test_ScriptPricingFromScript(t *testing.T) {
	const (
		mockAPIResponse = `{"akash-network":{"usd":3.57}}`
	)

	expectedPrice := fmt.Sprintf("%.*f", DefaultPricePrecision, 67843137.254901960)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := io.WriteString(w, mockAPIResponse)
		require.NoError(t, err)
	}))
	defer server.Close()

	err := os.Setenv("API_URL", server.URL)
	require.NoError(t, err)

	scriptPath, err := filepath.Abs("../script/usd_pricing_oracle.sh")
	require.NoError(t, err)

	pricing, err := MakeShellScriptPricing(scriptPath, 1, 30000*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	gspec := defaultGroupSpec()
	gspec.Resources[0].Resources.Endpoints = make(atypes.Endpoints, 7)
	req := Request{
		Owner: testutil.AccAddress(t).String(),
		GSpec: gspec,
	}

	price, err := pricing.CalculatePrice(context.Background(), req, nil)
	require.NoError(t, err)
	amount, err := sdk.NewDecFromStr(expectedPrice)
	require.NoError(t, err)

	require.Equal(t, sdk.NewDecCoinFromDec("uakt", amount).String(), price.String())
}

var _ ctypes.Reservation = (*testReservation)(nil)

type testReservation struct {
	ru dtypes.ResourceUnits
}

func (*testReservation) OrderID() mtypes.OrderID {
	return mtypes.OrderID{}
}

func (*testReservation) Resources() dtypes.ResourceGroup {
	return nil
}

func (r *testReservation) GetAllocatedResources() dtypes.ResourceUnits {
	return r.ru
}

func (r *testReservation) SetAllocatedResources(ru dtypes.ResourceUnits) {
	r.ru = ru
}

func (*testReservation) Allocated() bool      { return false }
func (*testReservation) ClusterParams() any   { return nil }
func (*testReservation) SetClusterParams(any) {}

func TestRationalToIntConversion(t *testing.T) {
	x := ceilBigRatToBigInt(big.NewRat(0, 1))
	require.Equal(t, big.NewInt(0), x)

	y := ceilBigRatToBigInt(big.NewRat(1, 1))
	require.Equal(t, big.NewInt(1), y)

	z := ceilBigRatToBigInt(big.NewRat(1, 2))
	require.Equal(t, big.NewInt(1), z)

	a := ceilBigRatToBigInt(big.NewRat(3, 2))
	require.Equal(t, big.NewInt(2), a)
}
