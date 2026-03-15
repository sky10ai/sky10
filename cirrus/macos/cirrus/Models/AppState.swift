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
    @Published var conflictPath: String? = nil

    let client: SkyClientProtocol
    let daemonManager: DaemonManager
    let activityLog = ActivityLog()

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
            activityLog.logUpload(path: remotePath, size: 0)
            await refresh()
        } catch {
            self.error = error.localizedDescription
            syncState = .error
            activityLog.logError(path: remotePath, message: error.localizedDescription)
        }
    }

    func downloadFile(remotePath: String, localPath: String) async {
        syncState = .syncing
        do {
            try await client.getFile(path: remotePath, outPath: localPath)
            activityLog.logDownload(path: remotePath, size: 0)
            syncState = .synced
        } catch {
            self.error = error.localizedDescription
            syncState = .error
            activityLog.logError(path: remotePath, message: error.localizedDescription)
        }
    }

    func removeFile(path: String) async {
        do {
            try await client.removeFile(path: path)
            activityLog.logDelete(path: path)
            await refresh()
        } catch {
            self.error = error.localizedDescription
            activityLog.logError(path: path, message: error.localizedDescription)
        }
    }

    func handleConflict(path: String) {
        conflictPath = path
        activityLog.logConflict(path: path)
    }

    // MARK: - Sync

    @Published var isSyncing = false
    @Published var syncDir: String = ""

    func startSync(dir: String) async {
        syncState = .syncing
        do {
            try await client.startSync(dir: dir, pollSeconds: 30)
            isSyncing = true
            syncDir = dir
            syncState = .synced
        } catch {
            self.error = error.localizedDescription
            syncState = .error
        }
    }

    func stopSync() async {
        do {
            try await client.stopSync()
            isSyncing = false
            syncDir = ""
            syncState = .offline
        } catch {
            self.error = error.localizedDescription
        }
    }

    func checkSyncStatus() async {
        do {
            let status = try await client.syncStatus()
            isSyncing = status.syncing
            syncDir = status.syncDir ?? ""
            if status.syncing {
                syncState = .synced
            }
        } catch {
            // Ignore — status check is best-effort
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
