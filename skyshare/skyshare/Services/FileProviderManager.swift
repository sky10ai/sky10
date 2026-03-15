import FileProvider
import Foundation

/// Manages the File Provider domain registration.
/// Registers "Sky" as a location in Finder's sidebar.
class FileProviderManager {
    static let domainIdentifier = NSFileProviderDomainIdentifier("ai.sky10.skyshare.fileprovider")
    static let domainName = "Sky"

    /// Register the file provider domain so "Sky" appears in Finder sidebar.
    static func register() {
        let domain = NSFileProviderDomain(
            identifier: domainIdentifier,
            displayName: domainName
        )

        NSFileProviderManager.add(domain) { error in
            if let error = error {
                print("Failed to register file provider domain: \(error)")
            }
        }
    }

    /// Remove the file provider domain from Finder sidebar.
    static func unregister() {
        let domain = NSFileProviderDomain(
            identifier: domainIdentifier,
            displayName: domainName
        )

        NSFileProviderManager.remove(domain) { error in
            if let error = error {
                print("Failed to remove file provider domain: \(error)")
            }
        }
    }

    /// Signal Finder that content has changed and needs re-enumeration.
    static func signalChange() {
        let domain = NSFileProviderDomain(
            identifier: domainIdentifier,
            displayName: domainName
        )

        NSFileProviderManager(for: domain)?.signalEnumerator(for: .rootContainer) { error in
            if let error = error {
                print("Failed to signal file provider: \(error)")
            }
        }
    }
}
