#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#include <stdint.h>

/* NVML types, only what we need */
typedef int nvmlReturn_t;
typedef void* nvmlDevice_t;

#define NVML_SUCCESS 0

typedef struct {
    uint32_t certChainSize;
    uint32_t attestationCertChainSize;
    uint8_t  certChain[4096];
    uint8_t  attestationCertChain[5120];
} nvmlConfComputeGpuCertificate_t;

typedef struct {
    uint32_t isCecAttestationReportPresent;
    uint32_t attestationReportSize;
    uint32_t cecAttestationReportSize;
    uint8_t  nonce[32];
    uint8_t  attestationReport[8192];
    uint8_t  cecAttestationReport[4096];
} nvmlConfComputeGpuAttestationReport_t;

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

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: nvml_attestation <probe|attest|cert> [args]\n");
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

    nvmlDevice_t device = NULL;
    uint32_t devIdx = 0;
    for (uint32_t i = 0; i < count; i++) {
        nvmlDevice_t d;
        if (nvmlDeviceGetHandle(i, &d) != NVML_SUCCESS) continue;
        if (nvmlDeviceGetCert) {
            nvmlConfComputeGpuCertificate_t cert;
            memset(&cert, 0, sizeof(cert));
            if (nvmlDeviceGetCert(d, &cert) == NVML_SUCCESS && cert.attestationCertChainSize > 0) {
                device = d;
                devIdx = i;
                break;
            }
        }
        if (!device) {
            device = d;
            devIdx = i;
        }
    }

    if (strcmp(cmd, "probe") == 0) {
        if (device && nvmlDeviceGetCert) {
            nvmlConfComputeGpuCertificate_t cert;
            memset(&cert, 0, sizeof(cert));
            if (nvmlDeviceGetCert(device, &cert) == NVML_SUCCESS && cert.attestationCertChainSize > 0) {
                fprintf(stdout, "GPU %u: CC enabled, cert chain %u bytes\n", devIdx, cert.attestationCertChainSize);
                nvmlShutdown();
                return 0;
            }
        }
        fprintf(stderr, "no CC-capable GPU found (%u devices checked)\n", count);
        nvmlShutdown();
        return 3;
    }

    if (strcmp(cmd, "attest") == 0) {
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

        nvmlConfComputeGpuAttestationReport_t report;
        memset(&report, 0, sizeof(report));

        int nonceLen = hex2bytes(argv[2], report.nonce, 32);
        if (nonceLen < 0) {
            fprintf(stderr, "invalid nonce hex\n");
            nvmlShutdown();
            return 1;
        }

        ret = nvmlDeviceGetAttReport(device, &report);
        if (ret != NVML_SUCCESS) {
            fprintf(stderr, "nvmlDeviceGetConfComputeGpuAttestationReport: %d\n", ret);
            nvmlShutdown();
            return 4;
        }

        uint8_t header[4];
        header[0] = report.attestationReportSize & 0xFF;
        header[1] = (report.attestationReportSize >> 8) & 0xFF;
        header[2] = (report.attestationReportSize >> 16) & 0xFF;
        header[3] = (report.attestationReportSize >> 24) & 0xFF;
        fwrite(header, 1, 4, stdout);
        fwrite(report.attestationReport, 1, report.attestationReportSize, stdout);

        if (report.isCecAttestationReportPresent && report.cecAttestationReportSize > 0) {
            header[0] = report.cecAttestationReportSize & 0xFF;
            header[1] = (report.cecAttestationReportSize >> 8) & 0xFF;
            header[2] = (report.cecAttestationReportSize >> 16) & 0xFF;
            header[3] = (report.cecAttestationReportSize >> 24) & 0xFF;
            fwrite(header, 1, 4, stdout);
            fwrite(report.cecAttestationReport, 1, report.cecAttestationReportSize, stdout);
        }

        nvmlShutdown();
        return 0;
    }

    if (strcmp(cmd, "cert") == 0) {
        if (!nvmlDeviceGetCert) {
            fprintf(stderr, "nvmlDeviceGetConfComputeGpuCertificate not available\n");
            nvmlShutdown();
            return 4;
        }
        nvmlConfComputeGpuCertificate_t cert;
        memset(&cert, 0, sizeof(cert));
        ret = nvmlDeviceGetCert(device, &cert);
        if (ret != NVML_SUCCESS) {
            fprintf(stderr, "nvmlDeviceGetConfComputeGpuCertificate: %d\n", ret);
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
