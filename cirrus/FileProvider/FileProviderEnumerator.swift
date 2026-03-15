import FileProvider
import Foundation

/// Enumerates skyfs files for the File Provider extension.
/// Connects to the Go backend via the same RPC client used by the main app.
class FileProviderEnumerator: NSObject, NSFileProviderEnumerator {
    private let containerIdentifier: NSFileProviderItemIdentifier
    private let rpc: RPCClient

    init(containerIdentifier: NSFileProviderItemIdentifier, rpc: RPCClient) {
        self.containerIdentifier = containerIdentifier
        self.rpc = rpc
        super.init()
    }

    func invalidate() {
        // Clean up if needed
    }

    func enumerateItems(for observer: NSFileProviderEnumerationObserver, startingAt page: NSFileProviderPage) {
        Task {
            do {
                let items = try await fetchItems()
                observer.didEnumerate(items)
                observer.finishEnumerating(upTo: nil)
            } catch {
                observer.finishEnumeratingWithError(error)
            }
        }
    }

    func enumerateChanges(for observer: NSFileProviderChangeObserver, from syncAnchor: NSFileProviderSyncAnchor) {
        // For now, report no changes — full enumeration on each access.
        // TODO: Use ops log timestamps as sync anchors for incremental updates.
        observer.finishEnumeratingChanges(upTo: syncAnchor, moreComing: false)
    }

    func currentSyncAnchor(completionHandler: @escaping (NSFileProviderSyncAnchor?) -> Void) {
        // Use current timestamp as sync anchor
        let anchor = NSFileProviderSyncAnchor(
            "\(Int(Date().timeIntervalSince1970))".data(using: .utf8)!
        )
        completionHandler(anchor)
    }

    private func fetchItems() async throws -> [NSFileProviderItem] {
        let prefix: String
        switch containerIdentifier {
        case .rootContainer:
            prefix = ""
        default:
            prefix = containerIdentifier.rawValue + "/"
        }

        let result: ListResult = try await rpc.call("skyfs.list", params: ["prefix": prefix])

        if containerIdentifier == .rootContainer {
            // At root, show namespace folders
            var namespaces = Set<String>()
            for file in result.files {
                namespaces.insert(file.namespace)
            }
            return namespaces.sorted().map { FileProviderFolder(name: $0) }
        }

        // Inside a namespace/directory, show files
        let formatter = ISO8601DateFormatter()
        return result.files.map { file in
            let modified = formatter.date(from: file.modified) ?? Date()
            return FileProviderItem(
                path: file.path,
                size: file.size,
                modified: modified,
                checksum: file.checksum,
                namespace: file.namespace
            )
        }
    }
}

// Reuse the list result type from SkyClient
private struct ListResult: Codable {
    let files: [FileResult]
}

private struct FileResult: Codable {
    let path: String
    let size: Int64
    let modified: String
    let checksum: String
    let namespace: String
    let chunks: Int
}
