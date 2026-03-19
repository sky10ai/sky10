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
    func createDrive(name: String, path: String, namespace: String?) async throws -> SkyClient.DriveInfoResult
    func removeDrive(id: String) async throws
    func listDrives() async throws -> [SkyClient.DriveInfoResult]
    func startDrive(id: String) async throws
    func stopDrive(id: String) async throws
    func listDevices() async throws -> DeviceListResponse
    func removeDevice(pubkey: String) async throws
    func generateInvite() async throws -> String
    func joinInvite(inviteID: String) async throws -> String
    func syncActivity() async throws -> [SkyClient.SyncActivityEntry]
    func debugDump() async throws -> SkyClient.DebugDumpResult
}

struct DeviceInfo: Codable {
    let pubkey: String
    let name: String
    let joined: String
    let platform: String?
    let ip: String?
    let location: String?
}

struct DeviceListResponse {
    let devices: [DeviceInfo]
    let thisDevice: String
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
