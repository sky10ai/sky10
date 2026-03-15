import Foundation
@testable import cirrus

/// Mock client for testing without a Go backend.
class MockSkyClient: SkyClientProtocol {
    var files: [FileNode] = []
    var info = StoreInfo(id: "sky10qtestkey123", fileCount: 0, totalSize: 0, namespaces: [])
    var putCalls: [(path: String, localPath: String)] = []
    var getCalls: [(path: String, outPath: String)] = []
    var removeCalls: [String] = []
    var shouldError = false
    var errorMessage = "mock error"

    func listFiles(prefix: String) async throws -> [FileNode] {
        if shouldError { throw MockError.simulated(errorMessage) }
        if prefix.isEmpty { return files }
        return files.filter { $0.path.hasPrefix(prefix) }
    }

    func putFile(path: String, localPath: String) async throws {
        if shouldError { throw MockError.simulated(errorMessage) }
        putCalls.append((path: path, localPath: localPath))
        files.append(FileNode(
            id: path, path: path, name: (path as NSString).lastPathComponent,
            size: 100, modified: "2026-03-14T10:00:00Z", checksum: "abc123",
            namespace: "default", chunks: 1
        ))
        info = StoreInfo(
            id: info.id, fileCount: files.count,
            totalSize: Int64(files.count * 100), namespaces: Array(Set(files.map { $0.namespace }))
        )
    }

    func getFile(path: String, outPath: String) async throws {
        if shouldError { throw MockError.simulated(errorMessage) }
        getCalls.append((path: path, outPath: outPath))
    }

    func removeFile(path: String) async throws {
        if shouldError { throw MockError.simulated(errorMessage) }
        removeCalls.append(path)
        files.removeAll { $0.path == path }
        info = StoreInfo(
            id: info.id, fileCount: files.count,
            totalSize: Int64(files.count * 100), namespaces: Array(Set(files.map { $0.namespace }))
        )
    }

    func getInfo() async throws -> StoreInfo {
        if shouldError { throw MockError.simulated(errorMessage) }
        return info
    }

    var isSyncing = false
    var syncDir = ""

    func startSync(dir: String, pollSeconds: Int) async throws {
        if shouldError { throw MockError.simulated(errorMessage) }
        isSyncing = true
        syncDir = dir
    }

    func stopSync() async throws {
        if shouldError { throw MockError.simulated(errorMessage) }
        isSyncing = false
        syncDir = ""
    }

    func syncStatus() async throws -> SyncStatusInfo {
        return SyncStatusInfo(syncing: isSyncing, syncDir: isSyncing ? syncDir : nil)
    }
}

enum MockError: Error, LocalizedError {
    case simulated(String)
    var errorDescription: String? {
        switch self {
        case .simulated(let msg): return msg
        }
    }
}
