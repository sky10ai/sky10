import Foundation

/// Documentation content for the storage provider settings view.
///
/// Current GitHub path: cirrus/cirrus/Views/Settings/StorageProviderDocs.swift
/// Provider model:      cirrus/cirrus/Models/StorageProvider.swift
enum StorageProviderDocs {
    static let headline = "Encrypted Storage Backend"

    static let description = """
    cirrus encrypts your files locally before uploading. \
    The storage provider never sees your data — only opaque encrypted blobs. \
    Pick any S3-compatible provider. You can switch providers later without re-encrypting.
    """

    /// URL managed in ExternalLinks.swift — update there if the docs file moves.
    static let learnMoreURL = ExternalLinks.storageProviders
}
