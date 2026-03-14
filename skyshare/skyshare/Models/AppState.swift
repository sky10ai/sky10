import Foundation
import SwiftUI

/// Central app state shared across all views.
@MainActor
class AppState: ObservableObject {
    @Published var syncState: SyncState = .offline
    @Published var files: [FileNode] = []
    @Published var storeInfo: StoreInfo?
    @Published var selectedNamespace: String? = nil
    @Published var isLoading = false
    @Published var error: String?

    let client: SkyClientProtocol
    let daemonManager: DaemonManager

    init(client: SkyClientProtocol = SkyClient(), daemonManager: DaemonManager = DaemonManager()) {
        self.client = client
        self.daemonManager = daemonManager
    }

    func start() async {
        daemonManager.start()
        try? await Task.sleep(for: .milliseconds(500))
        await refresh()
    }

    func refresh() async {
        isLoading = true
        defer { isLoading = false }

        do {
            storeInfo = try await client.getInfo()
            let allFiles = try await client.listFiles(prefix: "")
            files = allFiles
            syncState = .synced
            error = nil
        } catch {
            self.error = error.localizedDescription
            syncState = .error
        }
    }

    func uploadFile(localPath: String, remotePath: String) async {
        syncState = .syncing
        do {
            try await client.putFile(path: remotePath, localPath: localPath)
            await refresh()
        } catch {
            self.error = error.localizedDescription
            syncState = .error
        }
    }

    func downloadFile(remotePath: String, localPath: String) async {
        syncState = .syncing
        do {
            try await client.getFile(path: remotePath, outPath: localPath)
            syncState = .synced
        } catch {
            self.error = error.localizedDescription
            syncState = .error
        }
    }

    func removeFile(path: String) async {
        do {
            try await client.removeFile(path: path)
            await refresh()
        } catch {
            self.error = error.localizedDescription
        }
    }

    var filteredFiles: [FileNode] {
        guard let ns = selectedNamespace else { return files }
        return files.filter { $0.namespace == ns }
    }

    var namespaces: [String] {
        Array(Set(files.map { $0.namespace })).sorted()
    }
}
