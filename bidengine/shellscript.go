package bidengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/akash-network/node/sdl"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/provider/cluster/util"
)

type shellScriptPricing struct {
	path         string
	processLimit chan int
	runtimeLimit time.Duration
}

func MakeShellScriptPricing(path string, processLimit uint, runtimeLimit time.Duration) (BidPricingStrategy, error) {
	if len(path) == 0 {
		return nil, errPathEmpty
	}
	if processLimit == 0 {
		return nil, errProcessLimitZero
	}
	if runtimeLimit == 0 {
		return nil, errProcessRuntimeLimitZero
	}

	result := shellScriptPricing{
		path:         path,
		processLimit: make(chan int, processLimit),
		runtimeLimit: runtimeLimit,
	}

	// Use the channel as a semaphore to limit the number of processes created for computing bid processes
	// Most platforms put a limit on the number of processes a user can open. Even if the limit is high
	// it isn't a good idea to open thousands of processes.
	for i := uint(0); i != processLimit; i++ {
		result.processLimit <- 0
	}

	return result, nil
}

func parseCPU(res *atypes.CPU) uint64 {
	return res.Units.Val.Uint64()
}

func parseMemory(res *atypes.Memory) uint64 {
	return res.Quantity.Val.Uint64()
}

func parseGPU(resource *atypes.GPU) gpuElement {
	res := gpuElement{
		Units: resource.Units.Value(),
		Attributes: gpuAttributes{
			Vendor: make(map[string]gpuVendorAttributes),
		},
	}

	for _, attr := range resource.Attributes {
		tokens := strings.Split(attr.Key, "/")

		// vendor/nvidia/model/a100
		switch tokens[0] {
		case "vendor":
			vendor := tokens[1]
			model := tokens[3]
			var ram *string

			// vendor/nvidia/model/a100/ram/80Gi
			if len(tokens) == 6 && tokens[4] == "ram" {
				ram = new(string)
				*ram = tokens[5]
			}

			res.Attributes.Vendor[vendor] = gpuVendorAttributes{
				Model: model,
				RAM:   ram,
			}
		default:
		}
	}

	return res
}

func parseStorage(resource atypes.Volumes) []storageElement {
	res := make([]storageElement, 0, len(resource))

	for _, storage := range resource {
		class := sdl.StorageEphemeral
		if attr := storage.Attributes; attr != nil {
			if cl, _ := attr.Find(sdl.StorageAttributeClass).AsString(); cl != "" {
				class = cl
			}
		}

		res = append(res, storageElement{
			Class: class,
			Size:  storage.Quantity.Val.Uint64(),
		})
	}

	return res
}

func (ssp shellScriptPricing) CalculatePrice(ctx context.Context, req Request) (sdk.DecCoin, error) {
	buf := &bytes.Buffer{}

	dataForScript := make([]dataForScriptElement, len(req.GSpec.Resources))

	// iterate over everything & sum it up
	for i, group := range req.GSpec.Resources {
		groupCount := group.Count

		cpuQuantity := parseCPU(group.Resources.CPU)
		gpuQuantity := parseGPU(group.Resources.GPU)
		memoryQuantity := parseMemory(group.Resources.Memory)
		storageQuantity := parseStorage(group.Resources.Storage)
		endpointQuantity := len(group.Resources.Endpoints)

		dataForScript[i] = dataForScriptElement{
			CPU:              cpuQuantity,
			GPU:              gpuQuantity,
			Memory:           memoryQuantity,
			Storage:          storageQuantity,
			Count:            groupCount,
			EndpointQuantity: endpointQuantity,
			IPLeaseQuantity:  util.GetEndpointQuantityOfResourceUnits(group.Resources, atypes.Endpoint_LEASED_IP),
		}
	}

	encoder := json.NewEncoder(buf)
	err := encoder.Encode(dataForScript)
	if err != nil {
		return sdk.DecCoin{}, err
	}

	// Take 1 from the channel
	<-ssp.processLimit
	defer func() {
		// Always return it when this function is complete
		ssp.processLimit <- 0
	}()

	processCtx, cancel := context.WithTimeout(ctx, ssp.runtimeLimit)
	defer cancel()
	cmd := exec.CommandContext(processCtx, ssp.path) //nolint:gosec
	cmd.Stdin = buf
	outputBuf := &bytes.Buffer{}
	cmd.Stdout = outputBuf
	stderrBuf := &bytes.Buffer{}
	cmd.Stderr = stderrBuf

	subprocEnv := os.Environ()
	subprocEnv = append(subprocEnv, fmt.Sprintf("AKASH_OWNER=%s", req.Owner))
	cmd.Env = subprocEnv

	err = cmd.Run()

	if ctxErr := processCtx.Err(); ctxErr != nil {
		return sdk.DecCoin{}, ctxErr
	}

	if err != nil {
		return sdk.DecCoin{}, fmt.Errorf("%w: script failure %s", err, stderrBuf.String())
	}

	// Decode the result
	decoder := json.NewDecoder(outputBuf)
	decoder.UseNumber()

	var priceNumber json.Number
	err = decoder.Decode(&priceNumber)
	if err != nil {
		return sdk.DecCoin{}, fmt.Errorf("%w: script failure %s", err, stderrBuf.String())
	}

	price, err := sdk.NewDecFromStr(priceNumber.String())
	if err != nil {
		return sdk.DecCoin{}, ErrBidQuantityInvalid
	}

	if price.IsZero() {
		return sdk.DecCoin{}, ErrBidZero
	}

	if price.IsNegative() {
		return sdk.DecCoin{}, ErrBidQuantityInvalid
	}

	if !price.LTE(sdk.MaxSortableDec) {
		return sdk.DecCoin{}, ErrBidQuantityInvalid
	}

	return sdk.NewDecCoinFromDec(denom, price), nil
}
