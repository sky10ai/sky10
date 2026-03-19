import Foundation
import SwiftUI

/// Central app state shared across all views.
@MainActor
class AppState: ObservableObject {
    @Published var syncState: SyncState = .offline
    @Published var files: [FileNode] = []
    @Published var storeInfo: StoreInfo?
    @Published var isLoading = false
    @Published var error: String?
    @Published var conflictPath: String? = nil
    @Published var onboardingComplete: Bool

    let client: SkyClientProtocol
    let daemonManager: DaemonManager
    let activityLog = ActivityLog()

    init(client: SkyClientProtocol = SkyClient(), daemonManager: DaemonManager = DaemonManager()) {
        self.client = client
        self.daemonManager = daemonManager

        // Check if config exists
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let configExists = FileManager.default.fileExists(atPath: "\(home)/.sky10/fs/config.json")
        self.onboardingComplete = configExists

        if configExists {
            Task { await start() }
        }
    }

    func start() async {
        daemonManager.start()
        // Backend takes ~5-6 seconds on first launch (S3 config loading)
        try? await Task.sleep(for: .seconds(7))
        await loadDrives()
        await loadActivity()
        await refresh()
        subscribeToEvents()
    }

    private func subscribeToEvents() {
        // Subscribe to push events — refresh UI only when daemon says state changed
        Task.detached { [weak self] in
            guard let self = self else { return }
            let rpc = RPCClient()
            while true {
                await rpc.subscribe { event in
                    Task { @MainActor in
                        if event == "state.changed" {
                            await self.loadDrives()
                            await self.loadActivity()
                            await self.refresh()
                        } else if event == "sync.active" {
                            self.syncState = .syncing
                        }
                    }
                }
                // Connection dropped — reconnect after 2 seconds
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }

    func refresh() async {
        isLoading = true
        defer { isLoading = false }

        do {
            storeInfo = try await client.getInfo()

            // Build file list from local filesystem for each drive
            var localFiles: [FileNode] = []
            let uploadingPaths = Set(pendingActivity.filter { $0.direction == "up" }.map { $0.path })
            let downloadingPaths = Set(pendingActivity.filter { $0.direction == "down" }.map { $0.path })

            for drive in drives {
                let driveFiles = scanLocalDirectory(drive.localPath, namespace: drive.namespace,
                                                     uploadingPaths: uploadingPaths,
                                                     downloadingPaths: downloadingPaths)
                localFiles.append(contentsOf: driveFiles)
            }

            files = localFiles

            // Check if daemon is actively syncing
            let status = try await client.syncStatus()
            syncState = status.syncing ? .syncing : .synced
            error = nil
        } catch {
            self.error = error.localizedDescription
            syncState = .error
        }
    }

    /// Scan a local drive directory and build FileNode objects from the filesystem.
    private func scanLocalDirectory(_ rootPath: String, namespace: String,
                                     uploadingPaths: Set<String>,
                                     downloadingPaths: Set<String>) -> [FileNode] {
        let fm = FileManager.default
        let rootURL = URL(fileURLWithPath: rootPath)
        guard let enumerator = fm.enumerator(at: rootURL,
                                              includingPropertiesForKeys: [.fileSizeKey, .contentModificationDateKey, .isRegularFileKey],
                                              options: [.skipsHiddenFiles]) else {
            return []
        }

        var result: [FileNode] = []
        let isoFormatter = ISO8601DateFormatter()

        for case let fileURL as URL in enumerator {
            guard let resourceValues = try? fileURL.resourceValues(forKeys: [.isRegularFileKey, .fileSizeKey, .contentModificationDateKey]),
                  let isFile = resourceValues.isRegularFile, isFile else {
                continue
            }

            let relativePath = fileURL.path.replacingOccurrences(of: rootPath + "/", with: "")
            let fileName = fileURL.lastPathComponent

            // Skip .sky10 internal files
            if relativePath.hasPrefix(".") { continue }

            let size = Int64(resourceValues.fileSize ?? 0)
            let modified = resourceValues.contentModificationDate ?? Date()

            var status: FileSyncStatus = .synced
            if uploadingPaths.contains(relativePath) {
                status = .uploading
            } else if downloadingPaths.contains(relativePath) {
                status = .downloading
            }

            result.append(FileNode(
                id: relativePath,
                path: relativePath,
                name: fileName,
                size: size,
                modified: isoFormatter.string(from: modified),
                checksum: "",
                namespace: namespace,
                chunks: 0,
                syncStatus: status
            ))
        }

        return result
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

    // MARK: - Drives

    @Published var drives: [SkyClient.DriveInfoResult] = []
    @Published var pendingActivity: [SkyClient.SyncActivityEntry] = []

    func loadDrives() async {
        do {
            drives = try await client.listDrives()
            if drives.contains(where: { $0.running }) {
                syncState = .synced
            }
        } catch {
            // Best effort
        }
    }

    func createDrive(name: String, path: String) async {
        do {
            let drive = try await client.createDrive(name: name, path: path, namespace: nil)
            drives.append(drive)
            activityLog.logUpload(path: "Drive \(name) created", size: 0)
            syncState = .synced
        } catch {
            self.error = error.localizedDescription
        }
    }

    func removeDrive(id: String) async {
        do {
            try await client.removeDrive(id: id)
            drives.removeAll { $0.id == id }
        } catch {
            self.error = error.localizedDescription
        }
    }

    func toggleDrive(id: String, running: Bool) async {
        do {
            if running {
                try await client.stopDrive(id: id)
            } else {
                try await client.startDrive(id: id)
            }
            await loadDrives()
        } catch {
            self.error = error.localizedDescription
        }
    }

    var namespaces: [String] {
        Array(Set(files.map { $0.namespace })).sorted()
    }

    // MARK: - Activity

    func loadActivity() async {
        do {
            pendingActivity = try await client.syncActivity()
        } catch {
            // Best effort
        }
    }
}
