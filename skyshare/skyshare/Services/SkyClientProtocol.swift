import Foundation

/// Protocol for skyfs backend operations. Enables mocking in tests.
protocol SkyClientProtocol {
    func listFiles(prefix: String) async throws -> [FileNode]
    func putFile(path: String, localPath: String) async throws
    func getFile(path: String, outPath: String) async throws
    func removeFile(path: String) async throws
    func getInfo() async throws -> StoreInfo
    func startSync(dir: String, pollSeconds: Int) async throws
    func stopSync() async throws
    func syncStatus() async throws -> SyncStatusInfo
}

struct SyncStatusInfo: Codable {
    let syncing: Bool
    let syncDir: String?

    enum CodingKeys: String, CodingKey {
        case syncing
        case syncDir = "sync_dir"
    }
}

extension SkyClient: SkyClientProtocol {}
