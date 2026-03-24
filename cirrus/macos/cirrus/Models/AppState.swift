import CryptoKit
import Foundation
import SwiftUI

/// Central app state shared across all views.
@MainActor
class AppState: ObservableObject {
    @Published var syncState: SyncState = .offline
    @Published var files: [FileNode] = []
    @Published var emptyDirs: [String] = []
    @Published var dirHashes: [String: String] = [:]
    @Published var storeInfo: StoreInfo?
    @Published var isLoading = false
    @Published var error: String?
    @Published var conflictPath: String? = nil
    @Published var onboardingComplete: Bool
    @Published var daemonConnected = false
    @Published var outboxPending = 0
    @Published var syncDetail: String = ""

    let client: SkyClientProtocol
    let daemonManager: DaemonManager
    let activityLog = ActivityLog()
    private var refreshTask: Task<Void, Never>?

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
        Task.detached { [weak self] in
            guard let self = self else { return }
            let rpc = RPCClient()
            while true {
                await rpc.subscribe { event, data in
                    Task { @MainActor [weak self] in
                        guard let self = self else { return }
                        switch event {
                        case "state.changed":
                            self.debouncedRefresh()

                        case "sync.active":
                            self.syncState = .syncing

                        case "poll.progress":
                            self.syncState = .syncing
                            let fetched = data?["fetched"] as? Int ?? 0
                            let drive = data?["drive"] as? String ?? ""
                            self.syncDetail = "\(drive): fetching ops (\(fetched))"

                        case "download.start":
                            self.syncState = .syncing
                            let total = data?["total"] as? Int ?? 0
                            let drive = data?["drive"] as? String ?? ""
                            self.syncDetail = "\(drive): downloading \(total) files"

                        case "download.progress":
                            let done = data?["done"] as? Int ?? 0
                            let total = data?["total"] as? Int ?? 0
                            let drive = data?["drive"] as? String ?? ""
                            let dlPath = data?["path"] as? String ?? ""
                            self.syncDetail = "\(drive): downloaded \(done)/\(total)"
                            let dlShort = dlPath.split(separator: "/").suffix(2).joined(separator: "/")
                            self.activityLog.logDownload(path: dlShort, size: 0)

                        case "upload.start":
                            self.syncState = .syncing
                            let total = data?["total"] as? Int ?? 0
                            let drive = data?["drive"] as? String ?? ""
                            let path = (data?["path"] as? String)?.split(separator: "/").last.map(String.init) ?? ""
                            self.syncDetail = "\(drive): uploading \(path) (\(total) pending)"

                        case "upload.complete":
                            let total = data?["total"] as? Int ?? 0
                            let drive = data?["drive"] as? String ?? ""
                            let path = data?["path"] as? String ?? ""
                            let op = data?["op"] as? String ?? "put"
                            if total <= 1 {
                                self.syncDetail = ""
                            } else {
                                self.syncDetail = "\(drive): \(total - 1) remaining"
                            }
                            // Log to activity
                            let shortPath = path.split(separator: "/").suffix(2).joined(separator: "/")
                            switch op {
                            case "delete":
                                self.activityLog.logDelete(path: shortPath)
                            case "symlink":
                                self.activityLog.add(.symlink, path: shortPath, detail: "Symlink synced — \(drive)")
                            default:
                                self.activityLog.logUpload(path: shortPath, size: 0)
                            }

                        case "sync.complete":
                            let dl = data?["downloaded"] as? Int ?? 0
                            let del = data?["deleted"] as? Int ?? 0
                            let failed = data?["failed"] as? Int ?? 0
                            let drive = data?["drive"] as? String ?? ""
                            if dl > 0 || del > 0 {
                                self.syncDetail = "\(drive): \(dl) downloaded, \(del) deleted"
                                self.activityLog.add(.synced, path: drive, detail: "\(dl) downloaded, \(del) deleted\(failed > 0 ? ", \(failed) failed" : "")")
                            } else {
                                self.syncDetail = ""
                            }
                            self.syncState = .synced
                            self.debouncedRefresh()

                        default:
                            break
                        }
                    }
                }
                // Connection dropped — reconnect after 2 seconds
                try? await Task.sleep(for: .seconds(2))
            }
        }
    }

    /// Coalesce rapid state.changed events into a single refresh.
    private func debouncedRefresh() {
        refreshTask?.cancel()
        refreshTask = Task {
            try? await Task.sleep(for: .milliseconds(500))
            guard !Task.isCancelled else { return }
            await loadDrives()
            await loadActivity()
            await refresh()
        }
    }

    func refresh() async {
        isLoading = true
        defer { isLoading = false }

        do {
            storeInfo = try await client.getInfo()

            // Build file list from local filesystem for each drive
            var localFiles: [FileNode] = []
            var localEmptyDirs: [String] = []
            let uploadingPaths = Set(pendingActivity.filter { $0.direction == "up" }.map { $0.path })
            let downloadingPaths = Set(pendingActivity.filter { $0.direction == "down" }.map { $0.path })

            for drive in drives {
                let scan = scanLocalDirectory(drive.localPath, namespace: drive.namespace,
                                               uploadingPaths: uploadingPaths,
                                               downloadingPaths: downloadingPaths)
                localFiles.append(contentsOf: scan.files)
                localEmptyDirs.append(contentsOf: scan.emptyDirs)
            }

            files = localFiles
            emptyDirs = localEmptyDirs

            // Compute Merkle tree hashes for each drive
            var hashes: [String: String] = [:]
            for drive in drives {
                let tree = computeDirTree(drive.localPath)
                for (path, hash) in tree {
                    hashes[path] = hash
                }
            }
            dirHashes = hashes

            // Cross-check: log files that scanLocalDirectory found but merkleHash missed
            let missing = localFiles.filter { hashes[$0.path] == nil }
            if !missing.isEmpty {
                print("[cirrus] \(missing.count) files have no Merkle hash:")
                for f in missing.prefix(10) {
                    print("[cirrus]   \(f.path)")
                }
            }

            // Check daemon health
            let h = try await client.health()
            daemonConnected = true
            outboxPending = h.outboxPending
            syncState = h.outboxPending > 0 ? .syncing : .synced
            error = nil
        } catch {
            daemonConnected = false
            self.error = error.localizedDescription
            syncState = .error
        }
    }

    struct LocalScanResult {
        let files: [FileNode]
        let emptyDirs: [String]  // relative paths of empty directories
    }

    /// Scan a local drive directory and build FileNode objects from the filesystem.
    private func scanLocalDirectory(_ rootPath: String, namespace: String,
                                     uploadingPaths: Set<String>,
                                     downloadingPaths: Set<String>) -> LocalScanResult {
        let fm = FileManager.default
        let rootURL = URL(fileURLWithPath: rootPath)
        guard let enumerator = fm.enumerator(at: rootURL,
                                              includingPropertiesForKeys: [.fileSizeKey, .contentModificationDateKey, .isRegularFileKey, .isDirectoryKey],
                                              options: [.skipsHiddenFiles]) else {
            return LocalScanResult(files: [], emptyDirs: [])
        }

        var files: [FileNode] = []
        var dirPaths: [String] = []
        var dirsWithFiles: Set<String> = []
        let isoFormatter = ISO8601DateFormatter()

        for case let fileURL as URL in enumerator {
            let relativePath = fileURL.path.replacingOccurrences(of: rootPath + "/", with: "")
            if relativePath.hasPrefix(".") { continue }

            let resourceValues = try? fileURL.resourceValues(forKeys: [.isRegularFileKey, .isDirectoryKey, .fileSizeKey, .contentModificationDateKey])

            if resourceValues?.isDirectory == true {
                dirPaths.append(relativePath)
                continue
            }

            guard resourceValues?.isRegularFile == true else { continue }

            // Mark all parent directories as having files
            var parent = (relativePath as NSString).deletingLastPathComponent
            while !parent.isEmpty && parent != "." {
                dirsWithFiles.insert(parent)
                parent = (parent as NSString).deletingLastPathComponent
            }

            let size = Int64(resourceValues?.fileSize ?? 0)
            let modified = resourceValues?.contentModificationDate ?? Date()

            var status: FileSyncStatus = .synced
            if uploadingPaths.contains(relativePath) {
                status = .uploading
            } else if downloadingPaths.contains(relativePath) {
                status = .downloading
            }

            files.append(FileNode(
                id: relativePath,
                path: relativePath,
                name: fileURL.lastPathComponent,
                size: size,
                modified: isoFormatter.string(from: modified),
                checksum: "",
                namespace: namespace,
                chunks: 0,
                syncStatus: status
            ))
        }

        let emptyDirs = dirPaths.filter { !dirsWithFiles.contains($0) }
        return LocalScanResult(files: files, emptyDirs: emptyDirs)
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

    // MARK: - Merkle Tree

    /// Compute a Merkle tree of SHA-256 hashes for a directory.
    /// Returns a map of relative paths → hex hash strings.
    private func computeDirTree(_ rootPath: String) -> [String: String] {
        var tree: [String: String] = [:]
        _ = merkleHash(URL(fileURLWithPath: rootPath), root: URL(fileURLWithPath: rootPath), tree: &tree)
        return tree
    }

    private func merkleHash(_ dir: URL, root: URL, tree: inout [String: String]) -> String {
        let fm = FileManager.default
        let dirRel = dir.path.replacingOccurrences(of: root.path, with: "").trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard let entries = try? fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: [.isDirectoryKey, .isPackageKey], options: [.skipsHiddenFiles]) else {
            NSLog("[merkle] contentsOfDirectory FAILED for: %@", dirRel.isEmpty ? "." : dirRel)
            return SHA256.hash(data: Data()).compactMap { String(format: "%02x", $0) }.joined()
        }

        let sorted = entries.sorted { $0.lastPathComponent < $1.lastPathComponent }
        var hasher = SHA256()

        for entry in sorted {
            let rv = try? entry.resourceValues(forKeys: [.isDirectoryKey, .isPackageKey])
            let isDir = rv?.isDirectory ?? false
            let isPkg = rv?.isPackage ?? false
            let name = entry.lastPathComponent
            let rel = entry.path.replacingOccurrences(of: root.path + "/", with: "")

            // Treat as directory if isDirectory OR isPackage OR the URL
            // has a directory path (fallback when resourceValues lies).
            let treatAsDir = isDir || isPkg || entry.hasDirectoryPath

            if treatAsDir {
                let sub = merkleHash(entry, root: root, tree: &tree)
                tree[rel] = sub
                hasher.update(data: Data((name + "/").utf8))
                hasher.update(data: Data(sub.utf8))
            } else {
                let fileHash = hashFile(entry)
                tree[rel] = fileHash
                hasher.update(data: Data(name.utf8))
                hasher.update(data: Data(fileHash.utf8))
            }
        }

        let digest = hasher.finalize()
        let hash = digest.compactMap { String(format: "%02x", $0) }.joined()

        let key = dirRel.isEmpty ? "." : dirRel
        tree[key] = hash

        return hash
    }

    private func hashFile(_ url: URL) -> String {
        guard let data = try? Data(contentsOf: url) else {
            return String(repeating: "0", count: 64)
        }
        let digest = SHA256.hash(data: data)
        return digest.compactMap { String(format: "%02x", $0) }.joined()
    }
}
