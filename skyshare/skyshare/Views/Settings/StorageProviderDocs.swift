import Foundation

/// Documentation content for the storage provider settings view.
///
/// If this file moves, update the "Learn More" link in StorageSettingsView
/// to point to the new location in the GitHub repo.
///
/// Current GitHub path: skyshare/skyshare/Views/Settings/StorageProviderDocs.swift
/// Provider model:      skyshare/skyshare/Models/StorageProvider.swift
enum StorageProviderDocs {
    static let headline = "Encrypted Storage Backend"

    static let description = """
    skyshare encrypts your files locally before uploading. \
    The storage provider never sees your data — only opaque encrypted blobs. \
    Pick any S3-compatible provider. You can switch providers later without re-encrypting.
    """

    /// Points to the provider docs in the sky10 GitHub repo.
    /// UPDATE THIS if the docs file moves.
    static let learnMoreURL = URL(string: "https://github.com/sky10ai/sky10/blob/main/docs/learned/dependencies.md")!
}
