//go:build linux

package tee

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeNVMLSource = `
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

#define NVML_SUCCESS 0
#define NVML_GPU_CERT_CHAIN_SIZE 0x1000
#define NVML_GPU_ATTESTATION_CERT_CHAIN_SIZE 0x1400
#define NVML_CC_GPU_ATTESTATION_REPORT_SIZE 0x2000
#define NVML_CC_GPU_CEC_ATTESTATION_REPORT_SIZE 0x1000
#define NVML_CC_GPU_CEC_NONCE_SIZE 0x20

typedef struct {
    uint32_t certChainSize;
    uint32_t attestationCertChainSize;
    uint8_t certChain[NVML_GPU_CERT_CHAIN_SIZE];
    uint8_t attestationCertChain[NVML_GPU_ATTESTATION_CERT_CHAIN_SIZE];
} nvmlConfComputeGpuCertificate_t;

typedef struct {
    uint32_t isCecAttestationReportPresent;
    uint32_t attestationReportSize;
    uint32_t cecAttestationReportSize;
    uint8_t nonce[NVML_CC_GPU_CEC_NONCE_SIZE];
    uint8_t attestationReport[NVML_CC_GPU_ATTESTATION_REPORT_SIZE];
    uint8_t cecAttestationReport[NVML_CC_GPU_CEC_ATTESTATION_REPORT_SIZE];
} nvmlConfComputeGpuAttestationReport_t;

static uint32_t certificateCalls[3];

nvmlReturn_t nvmlInit_v2(void) { return NVML_SUCCESS; }
nvmlReturn_t nvmlShutdown(void) { return NVML_SUCCESS; }

nvmlReturn_t nvmlDeviceGetCount_v2(uint32_t *count) {
    *count = 2;
    return NVML_SUCCESS;
}

nvmlReturn_t nvmlDeviceGetHandleByIndex_v2(uint32_t index, nvmlDevice_t *device) {
    *device = (void *)(uintptr_t)(index + 1);
    return NVML_SUCCESS;
}

nvmlReturn_t nvmlDeviceGetArchitecture(nvmlDevice_t device, uint32_t *architecture) {
    (void)device;
    *architecture = 10;
    return NVML_SUCCESS;
}

nvmlReturn_t nvmlDeviceGetUUID(nvmlDevice_t device, char *uuid, uint32_t length) {
    uint32_t index = (uint32_t)(uintptr_t)device - 1;
    int written = snprintf(uuid, length, "GPU-00000000-0000-0000-0000-%012u", index);
    return written > 0 && (uint32_t)written < length ? NVML_SUCCESS : 999;
}

nvmlReturn_t nvmlDeviceGetConfComputeGpuCertificate(
    nvmlDevice_t device,
    nvmlConfComputeGpuCertificate_t *certificate
) {
    uint32_t index = (uint32_t)(uintptr_t)device;
    certificateCalls[index]++;
    if (index == 2 && certificateCalls[index] > 1 && getenv("FAKE_NVML_FAIL_SECOND_CERT")) {
        return 999;
    }

    certificate->attestationCertChainSize = 5;
    memcpy(certificate->attestationCertChain, index == 1 ? "cert0" : "cert1", 5);
    return NVML_SUCCESS;
}

nvmlReturn_t nvmlDeviceGetConfComputeGpuAttestationReport(
    nvmlDevice_t device,
    nvmlConfComputeGpuAttestationReport_t *report
) {
    uint32_t index = (uint32_t)(uintptr_t)device;
    report->attestationReportSize = 4;
    memcpy(report->attestationReport, index == 1 ? "gpu0" : "gpu1", 4);
    report->isCecAttestationReportPresent = 1;
    report->cecAttestationReportSize = 4;
    memcpy(report->cecAttestationReport, index == 1 ? "cec0" : "cec1", 4);
    return NVML_SUCCESS;
}
`

func buildFakeNVMLHelper(t *testing.T) (string, string) {
	t.Helper()

	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc is required for the NVML helper integration test")
	}

	tmpDir := t.TempDir()
	fakeSource := filepath.Join(tmpDir, "fake_nvml.c")
	fakeLibrary := filepath.Join(tmpDir, "libnvidia-ml.so.1")
	helper := filepath.Join(tmpDir, "nvml_attestation")
	if err := os.WriteFile(fakeSource, []byte(fakeNVMLSource), 0o600); err != nil {
		t.Fatal(err)
	}

	helperSource, err := filepath.Abs(filepath.Join("..", "nvml-helper", "nvml_attestation.c"))
	if err != nil {
		t.Fatal(err)
	}

	compile := exec.Command(gcc, "-std=c11", "-Wall", "-Wextra", "-Werror", helperSource, "-ldl", "-o", helper)
	output, err := compile.CombinedOutput()
	if err != nil {
		t.Fatalf("compile NVML helper: %v\n%s", err, output)
	}

	compile = exec.Command(gcc, "-std=c11", "-Wall", "-Wextra", "-Werror", "-shared", "-fPIC", fakeSource, "-o", fakeLibrary)
	output, err = compile.CombinedOutput()
	if err != nil {
		t.Fatalf("compile fake NVML library: %v\n%s", err, output)
	}

	return helper, tmpDir
}

func TestNVMLHelperFramesTwoGPUs(t *testing.T) {
	helper, libraryDir := buildFakeNVMLHelper(t)
	cmd := exec.Command(helper, "attest-all", strings.Repeat("00", 32))
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+libraryDir)

	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	reports, err := parseMultiGPUOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("got %d reports, want 2", len(reports))
	}

	want := []GPUDeviceReport{
		{DeviceIndex: 0, Architecture: "BLACKWELL", UUID: "GPU-00000000-0000-0000-0000-000000000000", AttestationReport: []byte("gpu0"), CECReport: []byte("cec0"), CertificateChain: []byte("cert0")},
		{DeviceIndex: 1, Architecture: "BLACKWELL", UUID: "GPU-00000000-0000-0000-0000-000000000001", AttestationReport: []byte("gpu1"), CECReport: []byte("cec1"), CertificateChain: []byte("cert1")},
	}
	for i := range want {
		if reports[i].DeviceIndex != want[i].DeviceIndex ||
			reports[i].Architecture != want[i].Architecture ||
			reports[i].UUID != want[i].UUID ||
			!bytes.Equal(reports[i].AttestationReport, want[i].AttestationReport) ||
			!bytes.Equal(reports[i].CECReport, want[i].CECReport) ||
			!bytes.Equal(reports[i].CertificateChain, want[i].CertificateChain) {
			t.Errorf("report %d = %#v, want %#v", i, reports[i], want[i])
		}
	}
}

func TestNVMLHelperEmitsNothingWhenAnyGPUFails(t *testing.T) {
	helper, libraryDir := buildFakeNVMLHelper(t)
	cmd := exec.Command(helper, "attest-all", strings.Repeat("00", 32))
	cmd.Env = append(
		os.Environ(),
		"LD_LIBRARY_PATH="+libraryDir,
		"FAKE_NVML_FAIL_SECOND_CERT=1",
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("attest-all succeeded after the second GPU certificate failed")
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed batch emitted %d bytes of partial frame data", stdout.Len())
	}
	if !strings.Contains(stderr.String(), "GPU 1: nvmlDeviceGetConfComputeGpuCertificate") {
		t.Fatalf("missing second-GPU error in stderr: %s", stderr.String())
	}
}
