/*
 * nvml_attestation — Minimal helper for GPU CC attestation via NVML.
 *
 * This binary dlopen's libnvidia-ml.so.1 and calls the NVML APIs to:
 *   probe      — Check if CC mode is enabled on any GPU
 *   attest     — Collect GPU attestation report from a single device (GPU 0)
 *   attest-all — Collect GPU attestation reports from ALL CC-capable devices
 *   cert       — Collect GPU attestation certificate chain from GPU 0
 *
 * It runs inside a chroot of the Kata guest rootfs so it picks up the
 * driver-matched libnvidia-ml.so. Output is binary on stdout.
 *
 * Usage:
 *   nvml_attestation probe
 *   nvml_attestation attest <hex-nonce>
 *   nvml_attestation attest-all <hex-nonce>
 *   nvml_attestation cert
 *
 * Binary output format for attest / attest-all:
 *   For each device:
 *     4 bytes LE: device index
 *     4 bytes LE: attestation report size
 *     N bytes:    attestation report
 *     4 bytes LE: CEC report size (0 if not present)
 *     N bytes:    CEC report (omitted if size is 0)
 *     4 bytes LE: certificate chain size
 *     M bytes:    PEM-encoded certificate chain
 *   attest outputs exactly one device; attest-all outputs all CC-capable devices.
 *
 * Exit codes:
 *   0 = success
 *   1 = usage error
 *   2 = NVML load/init error
 *   3 = no CC-capable GPU found
 *   4 = attestation/cert collection error
 */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <stdint.h>

/* NVML types — only what we need */
typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

#define NVML_SUCCESS 0
#define MAX_GPU_DEVICES 64
#define NVML_GPU_CERT_CHAIN_SIZE 0x1000
#define NVML_GPU_ATTESTATION_CERT_CHAIN_SIZE 0x1400
#define NVML_CC_GPU_ATTESTATION_REPORT_SIZE 0x2000
#define NVML_CC_GPU_CEC_ATTESTATION_REPORT_SIZE 0x1000
#define NVML_CC_GPU_CEC_NONCE_SIZE 0x20

typedef struct {
    uint32_t certChainSize;
    uint32_t attestationCertChainSize;
    uint8_t  certChain[NVML_GPU_CERT_CHAIN_SIZE];
    uint8_t  attestationCertChain[NVML_GPU_ATTESTATION_CERT_CHAIN_SIZE];
} nvmlConfComputeGpuCertificate_t;

typedef struct {
    uint32_t isCecAttestationReportPresent;
    uint32_t attestationReportSize;
    uint32_t cecAttestationReportSize;
    uint8_t  nonce[NVML_CC_GPU_CEC_NONCE_SIZE];
    uint8_t  attestationReport[NVML_CC_GPU_ATTESTATION_REPORT_SIZE];
    uint8_t  cecAttestationReport[NVML_CC_GPU_CEC_ATTESTATION_REPORT_SIZE];
} nvmlConfComputeGpuAttestationReport_t;

/* Function pointer types */
typedef nvmlReturn_t (*fn_init)(void);
typedef nvmlReturn_t (*fn_shutdown)(void);
typedef nvmlReturn_t (*fn_deviceGetCount)(uint32_t*);
typedef nvmlReturn_t (*fn_deviceGetHandleByIndex)(uint32_t, nvmlDevice_t*);
typedef nvmlReturn_t (*fn_deviceGetCert)(nvmlDevice_t, nvmlConfComputeGpuCertificate_t*);
typedef nvmlReturn_t (*fn_deviceGetAttReport)(nvmlDevice_t, nvmlConfComputeGpuAttestationReport_t*);

static int hex2bytes(const char *hex, uint8_t *out, int maxlen) {
    int len = strlen(hex);
    if (len % 2 != 0 || len/2 > maxlen) return -1;
    for (int i = 0; i < len/2; i++) {
        unsigned int b;
        if (sscanf(hex + 2*i, "%02x", &b) != 1) return -1;
        out[i] = (uint8_t)b;
    }
    return len/2;
}

static void write_le32(uint32_t val) {
    uint8_t buf[4];
    buf[0] = val & 0xFF;
    buf[1] = (val >> 8) & 0xFF;
    buf[2] = (val >> 16) & 0xFF;
    buf[3] = (val >> 24) & 0xFF;
    fwrite(buf, 1, 4, stdout);
}

/* Attest a single device and write its report to stdout.
 * Returns 0 on success, non-zero on failure. */
static int attest_device(
    fn_deviceGetAttReport nvmlDeviceGetAttReport,
    fn_deviceGetCert nvmlDeviceGetCert,
    nvmlDevice_t device,
    uint32_t devIdx,
    const uint8_t *nonce,
    int nonceLen
) {
    nvmlConfComputeGpuAttestationReport_t report;
    memset(&report, 0, sizeof(report));
    memcpy(report.nonce, nonce,
           nonceLen < NVML_CC_GPU_CEC_NONCE_SIZE ? nonceLen : NVML_CC_GPU_CEC_NONCE_SIZE);

    nvmlReturn_t ret = nvmlDeviceGetAttReport(device, &report);
    if (ret != NVML_SUCCESS) {
        fprintf(stderr, "GPU %u: nvmlDeviceGetConfComputeGpuAttestationReport: %d\n", devIdx, ret);
        return ret;
    }

    /* Driver-reported sizes are untrusted. Truncation would produce evidence
     * that no longer matches the signed report, so reject it instead. */
    if (report.attestationReportSize > sizeof(report.attestationReport)) {
        fprintf(stderr, "GPU %u: attestation report size %u exceeds buffer size %zu\n",
                devIdx, report.attestationReportSize, sizeof(report.attestationReport));
        return 1;
    }
    if (report.cecAttestationReportSize > sizeof(report.cecAttestationReport)) {
        fprintf(stderr, "GPU %u: CEC report size %u exceeds buffer size %zu\n",
                devIdx, report.cecAttestationReportSize, sizeof(report.cecAttestationReport));
        return 1;
    }

    /* Write device index */
    write_le32(devIdx);

    /* Write attestation report */
    write_le32(report.attestationReportSize);
    fwrite(report.attestationReport, 1, report.attestationReportSize, stdout);

    /* Write CEC report (size=0 if not present) */
    if (report.isCecAttestationReportPresent && report.cecAttestationReportSize > 0) {
        write_le32(report.cecAttestationReportSize);
        fwrite(report.cecAttestationReport, 1, report.cecAttestationReportSize, stdout);
    } else {
        write_le32(0);
    }

    /* Local verification requires the attestation certificate chain. A report
     * without it cannot be authenticated, so fail instead of returning partial
     * evidence that downstream code could accidentally accept. */
    if (!nvmlDeviceGetCert) {
        fprintf(stderr, "GPU %u: nvmlDeviceGetConfComputeGpuCertificate not available\n", devIdx);
        return 1;
    }

    nvmlConfComputeGpuCertificate_t cert;
    memset(&cert, 0, sizeof(cert));
    ret = nvmlDeviceGetCert(device, &cert);
    if (ret != NVML_SUCCESS || cert.attestationCertChainSize == 0) {
        fprintf(stderr, "GPU %u: nvmlDeviceGetConfComputeGpuCertificate: %d (size=%u)\n",
                devIdx, ret, cert.attestationCertChainSize);
        return 1;
    }
    if (cert.attestationCertChainSize > sizeof(cert.attestationCertChain)) {
        fprintf(stderr, "GPU %u: certificate chain size %u exceeds buffer size %zu\n",
                devIdx, cert.attestationCertChainSize, sizeof(cert.attestationCertChain));
        return 1;
    }
    write_le32(cert.attestationCertChainSize);
    fwrite(cert.attestationCertChain, 1, cert.attestationCertChainSize, stdout);

    return 0;
}

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: nvml_attestation <probe|attest|attest-all|cert> [args]\n");
        return 1;
    }

    const char *cmd = argv[1];

    void *lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY);
    if (!lib) {
        fprintf(stderr, "dlopen libnvidia-ml.so.1: %s\n", dlerror());
        return 2;
    }

    fn_init nvmlInit = dlsym(lib, "nvmlInit_v2");
    fn_shutdown nvmlShutdown = dlsym(lib, "nvmlShutdown");
    fn_deviceGetCount nvmlDeviceGetCount = dlsym(lib, "nvmlDeviceGetCount_v2");
    fn_deviceGetHandleByIndex nvmlDeviceGetHandle = dlsym(lib, "nvmlDeviceGetHandleByIndex_v2");
    fn_deviceGetCert nvmlDeviceGetCert = dlsym(lib, "nvmlDeviceGetConfComputeGpuCertificate");
    fn_deviceGetAttReport nvmlDeviceGetAttReport = dlsym(lib, "nvmlDeviceGetConfComputeGpuAttestationReport");

    if (!nvmlInit || !nvmlShutdown || !nvmlDeviceGetCount || !nvmlDeviceGetHandle) {
        fprintf(stderr, "failed to resolve core NVML symbols\n");
        return 2;
    }

    nvmlReturn_t ret = nvmlInit();
    if (ret != NVML_SUCCESS) {
        fprintf(stderr, "nvmlInit failed: %d\n", ret);
        return 2;
    }

    uint32_t count = 0;
    ret = nvmlDeviceGetCount(&count);
    if (ret != NVML_SUCCESS || count == 0) {
        fprintf(stderr, "no NVIDIA devices found (ret=%d, count=%u)\n", ret, count);
        nvmlShutdown();
        return 3;
    }
    if (count > MAX_GPU_DEVICES) {
        fprintf(stderr, "GPU count %u exceeds helper maximum %u\n", count, MAX_GPU_DEVICES);
        nvmlShutdown();
        return 4;
    }

    /* Build list of CC-capable devices */
    nvmlDevice_t devices[MAX_GPU_DEVICES];
    uint32_t     deviceIdxs[MAX_GPU_DEVICES];
    uint32_t     ccCount = 0;

    for (uint32_t i = 0; i < count; i++) {
        nvmlDevice_t d;
        if (nvmlDeviceGetHandle(i, &d) != NVML_SUCCESS) continue;
        if (nvmlDeviceGetCert) {
            nvmlConfComputeGpuCertificate_t cert;
            memset(&cert, 0, sizeof(cert));
            if (nvmlDeviceGetCert(d, &cert) == NVML_SUCCESS && cert.attestationCertChainSize > 0) {
                if (cert.attestationCertChainSize > sizeof(cert.attestationCertChain)) {
                    fprintf(stderr, "GPU %u: certificate chain size %u exceeds buffer size %zu\n",
                            i, cert.attestationCertChainSize, sizeof(cert.attestationCertChain));
                    nvmlShutdown();
                    return 4;
                }
                devices[ccCount] = d;
                deviceIdxs[ccCount] = i;
                ccCount++;
            }
        }
    }

    /* If no CC-capable devices found, fall back to first device for non-CC commands */
    nvmlDevice_t firstDevice = NULL;
    uint32_t firstIdx = 0;
    if (ccCount == 0) {
        for (uint32_t i = 0; i < count; i++) {
            nvmlDevice_t d;
            if (nvmlDeviceGetHandle(i, &d) == NVML_SUCCESS) {
                firstDevice = d;
                firstIdx = i;
                break;
            }
        }
    }

    if (strcmp(cmd, "probe") == 0) {
        if (ccCount > 0) {
            fprintf(stdout, "%u CC-capable GPU(s) found\n", ccCount);
            for (uint32_t i = 0; i < ccCount; i++) {
                fprintf(stdout, "  GPU %u: CC enabled\n", deviceIdxs[i]);
            }
            nvmlShutdown();
            return 0;
        }
        fprintf(stderr, "no CC-capable GPU found (%u devices checked)\n", count);
        nvmlShutdown();
        return 3;
    }

    if (strcmp(cmd, "attest") == 0) {
        /* Single-device attest (backwards compatible): attest first CC device */
        if (!nvmlDeviceGetAttReport) {
            fprintf(stderr, "nvmlDeviceGetConfComputeGpuAttestationReport not available\n");
            nvmlShutdown();
            return 4;
        }
        if (argc < 3) {
            fprintf(stderr, "usage: nvml_attestation attest <hex-nonce>\n");
            nvmlShutdown();
            return 1;
        }

        uint8_t nonce[NVML_CC_GPU_CEC_NONCE_SIZE];
        int nonceLen = hex2bytes(argv[2], nonce, NVML_CC_GPU_CEC_NONCE_SIZE);
        if (nonceLen != NVML_CC_GPU_CEC_NONCE_SIZE) {
            fprintf(stderr, "nonce must be exactly 32 bytes\n");
            nvmlShutdown();
            return 1;
        }

        nvmlDevice_t dev = ccCount > 0 ? devices[0] : firstDevice;
        uint32_t idx = ccCount > 0 ? deviceIdxs[0] : firstIdx;
        if (!dev) {
            fprintf(stderr, "no usable GPU device handle\n");
            nvmlShutdown();
            return 3;
        }

        int rc = attest_device(nvmlDeviceGetAttReport, nvmlDeviceGetCert, dev, idx, nonce, nonceLen);
        nvmlShutdown();
        return rc ? 4 : 0;
    }

    if (strcmp(cmd, "attest-all") == 0) {
        /* Multi-device attest: attest ALL CC-capable devices */
        if (!nvmlDeviceGetAttReport) {
            fprintf(stderr, "nvmlDeviceGetConfComputeGpuAttestationReport not available\n");
            nvmlShutdown();
            return 4;
        }
        if (argc < 3) {
            fprintf(stderr, "usage: nvml_attestation attest-all <hex-nonce>\n");
            nvmlShutdown();
            return 1;
        }
        if (ccCount == 0) {
            fprintf(stderr, "no CC-capable GPUs to attest\n");
            nvmlShutdown();
            return 3;
        }

        uint8_t nonce[NVML_CC_GPU_CEC_NONCE_SIZE];
        int nonceLen = hex2bytes(argv[2], nonce, NVML_CC_GPU_CEC_NONCE_SIZE);
        if (nonceLen != NVML_CC_GPU_CEC_NONCE_SIZE) {
            fprintf(stderr, "nonce must be exactly 32 bytes\n");
            nvmlShutdown();
            return 1;
        }

        /* Write device count header */
        write_le32(ccCount);

        int failures = 0;
        for (uint32_t i = 0; i < ccCount; i++) {
            if (attest_device(nvmlDeviceGetAttReport, nvmlDeviceGetCert, devices[i], deviceIdxs[i], nonce, nonceLen) != 0) {
                failures++;
            }
        }

        nvmlShutdown();
        return failures > 0 ? 4 : 0;
    }

    if (strcmp(cmd, "cert") == 0) {
        if (!nvmlDeviceGetCert) {
            fprintf(stderr, "nvmlDeviceGetConfComputeGpuCertificate not available\n");
            nvmlShutdown();
            return 4;
        }

        nvmlDevice_t dev = ccCount > 0 ? devices[0] : firstDevice;
        if (!dev) {
            fprintf(stderr, "no usable GPU device handle\n");
            nvmlShutdown();
            return 3;
        }

        nvmlConfComputeGpuCertificate_t cert;
        memset(&cert, 0, sizeof(cert));
        ret = nvmlDeviceGetCert(dev, &cert);
        if (ret != NVML_SUCCESS) {
            fprintf(stderr, "nvmlDeviceGetConfComputeGpuCertificate: %d\n", ret);
            nvmlShutdown();
            return 4;
        }

        if (cert.attestationCertChainSize > sizeof(cert.attestationCertChain)) {
            fprintf(stderr, "certificate chain size %u exceeds buffer size %zu\n",
                    cert.attestationCertChainSize, sizeof(cert.attestationCertChain));
            nvmlShutdown();
            return 4;
        }
        fwrite(cert.attestationCertChain, 1, cert.attestationCertChainSize, stdout);
        nvmlShutdown();
        return 0;
    }

    fprintf(stderr, "unknown command: %s\n", cmd);
    nvmlShutdown();
    return 1;
}
