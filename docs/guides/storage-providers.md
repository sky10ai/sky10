# Storage Providers

> **NOTE:** The skyshare macOS app links to this file from the "Learn More" button
> in Settings → Storage. If this file moves, update the URL in:
> `skyshare/skyshare/Services/ExternalLinks.swift`

skyfs works with any S3-compatible storage provider. Your files are encrypted
locally before upload — the provider only sees opaque encrypted blobs.

## Supported Providers

### Backblaze B2

- **Cost:** ~$6/TB/month storage, $0.01/GB egress
- **Endpoint:** `https://s3.{region}.backblazeb2.com`
- **Path style:** No
- **Regions:** us-west-004, us-west-002, eu-central-003
- **Notes:** Cheapest general-purpose option. 10GB free tier. S3-compatible API
  enabled per bucket. Free egress via Cloudflare bandwidth alliance.
- **Setup:** Create a bucket with "App Keys" for S3 credentials.
  The `keyID` is your access key, `applicationKey` is your secret.

### Cloudflare R2

- **Cost:** ~$15/TB/month storage, zero egress fees
- **Endpoint:** `https://{account_id}.r2.cloudflarestorage.com`
- **Path style:** Yes
- **Regions:** Automatic (no region selection)
- **Notes:** No egress fees ever — best if you read data frequently from
  multiple devices. Requires Cloudflare account ID in the endpoint.
- **Setup:** Create an R2 bucket, generate an API token with "Edit" permissions.
  Your account ID is in the Cloudflare dashboard URL.

### DigitalOcean Spaces

- **Cost:** $5/month for 250GB + 1TB transfer
- **Endpoint:** `https://{region}.digitaloceanspaces.com`
- **Path style:** No
- **Regions:** nyc3, sfo3, ams3, sgp1, fra1, syd1
- **Notes:** Simple flat pricing. Includes CDN. Good for small-to-medium usage.
- **Setup:** Create a Space, generate Spaces API keys from the API settings.

### AWS S3

- **Cost:** ~$23/TB/month (Standard), $0.09/GB egress
- **Endpoint:** `https://s3.{region}.amazonaws.com`
- **Path style:** No
- **Regions:** us-east-1, us-east-2, us-west-1, us-west-2, eu-west-1,
  eu-west-2, eu-central-1, ap-southeast-1, ap-northeast-1, and many more
- **Notes:** The original. Most features, highest cost. Use S3 Glacier for
  archival. IAM policies for fine-grained access control.
- **Setup:** Create an S3 bucket, generate IAM access keys with S3 permissions.

### Wasabi

- **Cost:** $7/TB/month, no egress fees, no API fees
- **Endpoint:** `https://s3.{region}.wasabisys.com`
- **Path style:** No
- **Regions:** us-east-1, us-east-2, us-west-1, us-central-1, eu-central-1,
  eu-central-2, eu-west-1, eu-west-2, ap-northeast-1, ap-southeast-1
- **Notes:** Flat pricing with no egress or API charges. 90-day minimum
  storage duration (deleted files still billed for 90 days).
- **Setup:** Create a bucket, generate access keys from the Wasabi console.

### MinIO (Self-Hosted)

- **Cost:** Hardware only
- **Endpoint:** User-provided (e.g. `http://localhost:9000`)
- **Path style:** Yes
- **Regions:** N/A (default: us-east-1)
- **Notes:** Run your own S3-compatible server. Full control over data
  location. Supports bucket notifications via NATS, Redis, Kafka, webhooks.
  Best option for privacy-maximalist setups.
- **Setup:** Install MinIO, create a bucket, use the root credentials
  or create a service account.

## Choosing a Provider

| Priority | Recommendation |
|----------|---------------|
| Cheapest storage | Backblaze B2 |
| No egress fees | Cloudflare R2 or Wasabi |
| Self-hosted / full control | MinIO |
| Simplest setup | DigitalOcean Spaces |
| Most features | AWS S3 |

## Credentials

skyfs uses S3-compatible credentials:

```bash
export S3_ACCESS_KEY_ID=your-key
export S3_SECRET_ACCESS_KEY=your-secret
```

The skyshare app stores credentials in the macOS Keychain — they never
touch the filesystem.

## Switching Providers

You can switch providers at any time. Your data is encrypted with your
identity key, not the provider's infrastructure. To migrate:

1. Set up the new provider in skyshare settings
2. Upload your files to the new bucket
3. Remove the old bucket when ready

File keys and namespace keys are stored in the bucket alongside the
encrypted data, so everything moves together.
