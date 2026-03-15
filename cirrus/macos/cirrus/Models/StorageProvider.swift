import Foundation

/// Pre-configured S3-compatible storage providers.
struct StorageProvider: Identifiable, Hashable {
    let id: String
    let name: String
    let icon: String
    let endpointTemplate: String  // {region} and {account_id} are replaced
    let forcePathStyle: Bool
    let regions: [ProviderRegion]
    let helpURL: String
    let needsAccountID: Bool

    struct ProviderRegion: Identifiable, Hashable {
        let id: String
        let label: String
    }
}

extension StorageProvider {
    static let all: [StorageProvider] = [backblaze, cloudflare, digitalOcean, aws, wasabi, minio]

    static let backblaze = StorageProvider(
        id: "backblaze",
        name: "Backblaze B2",
        icon: "flame",
        endpointTemplate: "https://s3.{region}.backblazeb2.com",
        forcePathStyle: false,
        regions: [
            .init(id: "us-west-004", label: "US West (Sacramento)"),
            .init(id: "us-west-002", label: "US West (Phoenix)"),
            .init(id: "eu-central-003", label: "EU Central (Amsterdam)"),
        ],
        helpURL: "https://www.backblaze.com/docs/cloud-storage-s3-compatible-api",
        needsAccountID: false
    )

    static let cloudflare = StorageProvider(
        id: "cloudflare",
        name: "Cloudflare R2",
        icon: "cloud",
        endpointTemplate: "https://{account_id}.r2.cloudflarestorage.com",
        forcePathStyle: true,
        regions: [
            .init(id: "auto", label: "Automatic"),
        ],
        helpURL: "https://developers.cloudflare.com/r2/api/s3/api/",
        needsAccountID: true
    )

    static let digitalOcean = StorageProvider(
        id: "digitalocean",
        name: "DigitalOcean Spaces",
        icon: "drop",
        endpointTemplate: "https://{region}.digitaloceanspaces.com",
        forcePathStyle: false,
        regions: [
            .init(id: "nyc3", label: "New York 3"),
            .init(id: "sfo3", label: "San Francisco 3"),
            .init(id: "ams3", label: "Amsterdam 3"),
            .init(id: "sgp1", label: "Singapore 1"),
            .init(id: "fra1", label: "Frankfurt 1"),
            .init(id: "syd1", label: "Sydney 1"),
            .init(id: "atl1", label: "Atlanta 1"),
            .init(id: "blr1", label: "Bangalore 1"),
            .init(id: "lon1", label: "London 1"),
            .init(id: "tor1", label: "Toronto 1"),
        ],
        helpURL: "https://docs.digitalocean.com/products/spaces/",
        needsAccountID: false
    )

    static let aws = StorageProvider(
        id: "aws",
        name: "AWS S3",
        icon: "server.rack",
        endpointTemplate: "https://s3.{region}.amazonaws.com",
        forcePathStyle: false,
        regions: [
            .init(id: "us-east-1", label: "US East (N. Virginia)"),
            .init(id: "us-east-2", label: "US East (Ohio)"),
            .init(id: "us-west-1", label: "US West (N. California)"),
            .init(id: "us-west-2", label: "US West (Oregon)"),
            .init(id: "eu-west-1", label: "EU (Ireland)"),
            .init(id: "eu-west-2", label: "EU (London)"),
            .init(id: "eu-central-1", label: "EU (Frankfurt)"),
            .init(id: "ap-southeast-1", label: "Asia Pacific (Singapore)"),
            .init(id: "ap-northeast-1", label: "Asia Pacific (Tokyo)"),
        ],
        helpURL: "https://aws.amazon.com/s3/",
        needsAccountID: false
    )

    static let wasabi = StorageProvider(
        id: "wasabi",
        name: "Wasabi",
        icon: "leaf",
        endpointTemplate: "https://s3.{region}.wasabisys.com",
        forcePathStyle: false,
        regions: [
            .init(id: "us-east-1", label: "US East (N. Virginia)"),
            .init(id: "us-east-2", label: "US East (N. Virginia 2)"),
            .init(id: "us-west-1", label: "US West (Oregon)"),
            .init(id: "us-central-1", label: "US Central (Texas)"),
            .init(id: "eu-central-1", label: "EU (Amsterdam)"),
            .init(id: "eu-central-2", label: "EU (Frankfurt)"),
            .init(id: "eu-west-1", label: "EU (London)"),
            .init(id: "eu-west-2", label: "EU (Paris)"),
            .init(id: "ap-northeast-1", label: "AP (Tokyo)"),
            .init(id: "ap-southeast-1", label: "AP (Singapore)"),
        ],
        helpURL: "https://wasabi.com/s3-compatible-cloud-storage/",
        needsAccountID: false
    )

    static let minio = StorageProvider(
        id: "minio",
        name: "MinIO (Self-Hosted)",
        icon: "desktopcomputer",
        endpointTemplate: "",  // user provides full URL
        forcePathStyle: true,
        regions: [
            .init(id: "us-east-1", label: "Default"),
        ],
        helpURL: "https://min.io/docs/minio/linux/index.html",
        needsAccountID: false
    )

    /// Build the endpoint URL from the template and user inputs.
    func endpoint(region: String, accountID: String = "") -> String {
        if endpointTemplate.isEmpty { return "" }  // MinIO: user provides
        return endpointTemplate
            .replacingOccurrences(of: "{region}", with: region)
            .replacingOccurrences(of: "{account_id}", with: accountID)
    }
}
