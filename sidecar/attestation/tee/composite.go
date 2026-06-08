package tee

import (
	"context"
	"fmt"
)

// GPUCompositeProvider wraps a CPU TEE provider and an NvidiaGPUAttestor,
// collecting both CPU and GPU attestation evidence in a single GetQuote call.
//
// If the CPU attestation succeeds but GPU attestation fails, GetQuote returns
// an error — partial attestation is a security gap.
type GPUCompositeProvider struct {
	CPU Provider
	GPU *NvidiaGPUAttestor
}

var _ Provider = (*GPUCompositeProvider)(nil)

func (g *GPUCompositeProvider) Name() string {
	switch g.CPU.Name() {
	case NameTDX:
		return NameTDXGPU
	default:
		return NameSNPGPU
	}
}

func (g *GPUCompositeProvider) Available() bool {
	return g.CPU.Available() && g.GPU.Available()
}

func (g *GPUCompositeProvider) GetQuote(ctx context.Context, reportData [64]byte) (*QuoteResult, error) {
	result, err := g.CPU.GetQuote(ctx, reportData)
	if err != nil {
		return nil, fmt.Errorf("cpu attestation: %w", err)
	}

	gpuReport, err := g.GPU.GetGPUAttestation(ctx, reportData)
	if err != nil {
		return nil, fmt.Errorf("gpu attestation: %w", err)
	}

	result.GPUReport = gpuReport
	return result, nil
}
