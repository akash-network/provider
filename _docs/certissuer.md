# Certificate Issuer for REST and gRPC Gateways

This document describes how to configure and use the built-in certificate issuer for Akash Network provider gateways. The certificate issuer supports automatic SSL/TLS certificate generation and renewal using Let's Encrypt.

## Overview

The certificate issuer enables automatic SSL/TLS certificate management for your provider's REST and gRPC gateways, ensuring secure connections for clients.

## Configuration

### Command Line Flags

| Flag | Default | Description | Required |
|------|---------|-------------|----------|
| `--cert-issuer-enabled` | `false` | Enable the built-in certificate issuer | No |
| `--cert-issuer-ca-dir-url` | `https://acme-staging-v02.api.letsencrypt.org/directory` | ACME directory URL for certificate authority | No |
| `--cert-issuer-email` | - | Email address for ACME account registration | **Yes** |
| `--cert-issuer-dns-providers` | - | Comma-separated list of DNS providers (`cf`, `gcloud`) | No |

### Environment Configuration

**Important**: DNS provider configuration is currently only possible via environment variables.

## DNS Providers

The certificate issuer supports multiple DNS providers for domain validation. Choose the provider that matches your domain's DNS hosting service.

### Cloudflare

**Provider name**: `cf`

#### Configuration

Set the following environment variable:

```bash
export CF_DNS_API_TOKEN="your_cloudflare_api_token"
```

**Requirements**:
- API token must have `DNS:Edit` permission
- Token can be created in Cloudflare dashboard under **My Profile** → **API Tokens**

### Google Cloud DNS

**Provider name**: `gcloud`

#### Configuration

Choose one of the following options:

**Option 1: Service Account JSON (Recommended)**
```bash
export GCE_SERVICE_ACCOUNT='{"type": "service_account", "project_id": "...", ...}'
```

**Option 2: Service Account File Path**
```bash
export GCE_SERVICE_ACCOUNT_FILE="/path/to/service-account.json"
```

**Requirements**:
- Service account must have `DNS Administrator` role or equivalent permissions
- Service account JSON can be downloaded from Google Cloud Console

## Usage Examples

### Basic Setup with Let's Encrypt Production

```bash
./akash-provider \
  --cert-issuer-enabled=true \
  --cert-issuer-email="admin@yourdomain.com" \
  --cert-issuer-ca-dir-url="https://acme-v02.api.letsencrypt.org/directory" \
  --cert-issuer-dns-providers="cf"
```

### Using Google Cloud DNS

```bash
export GCE_SERVICE_ACCOUNT_FILE="/path/to/service-account.json"

./akash-provider \
  --cert-issuer-enabled=true \
  --cert-issuer-email="admin@yourdomain.com" \
  --cert-issuer-dns-providers="gcloud"
```

## Notes

- **Staging Environment**: The default configuration uses Let's Encrypt's staging environment, which has higher rate limits and is suitable for testing
- **Production Environment**: For production use, change the CA directory URL to `https://acme-v02.api.letsencrypt.org/directory`
- **DNS Validation**: The certificate issuer uses DNS-01 challenge validation, which requires DNS provider access
- **Rate Limits**: Let's Encrypt has rate limits; use staging environment for testing to avoid hitting production limits
