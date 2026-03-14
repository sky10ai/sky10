import Foundation

/// Centralized registry of all external URLs referenced in the app.
///
/// If a linked file moves in the GitHub repo, update the URL here.
/// The test suite validates that every URL returns HTTP 200.
enum ExternalLinks {
    /// Storage provider guide — linked from Settings → Storage → "Learn More"
    /// Source: docs/learned/storage-providers.md
    static let storageProviders = URL(string: "https://github.com/sky10ai/sky10/blob/main/docs/learned/storage-providers.md")!

    /// Dependency decisions — linked from developer docs
    /// Source: docs/learned/dependencies.md
    static let dependencies = URL(string: "https://github.com/sky10ai/sky10/blob/main/docs/learned/dependencies.md")!

    /// Main repository
    static let repository = URL(string: "https://github.com/sky10ai/sky10")!

    /// All links for validation
    static let all: [URL] = [
        storageProviders,
        dependencies,
        repository,
    ]
}
