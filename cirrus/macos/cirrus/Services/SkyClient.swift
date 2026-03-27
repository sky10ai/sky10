import Foundation

/// High-level client for skyfs backend operations.
class SkyClient {
    private let rpc = RPCClient()

    struct ListResult: Codable {
        let files: [FileResult]
        let dirs: [DirResult]?
    }

    struct FileResult: Codable {
        let path: String
        let size: Int64
        let modified: String
        let checksum: String
        let namespace: String
        let chunks: Int
    }

    struct DirResult: Codable {
        let path: String
        let namespace: String?
    }

    struct ListFilesResult {
        let files: [FileNode]
        let dirs: [String]
    }

    func listFiles(prefix: String) async throws -> ListFilesResult {
        let result: ListResult = try await rpc.call("skyfs.list", params: ["prefix": prefix])
        let files = result.files.map { f in
            FileNode(
                id: f.path, path: f.path,
                name: (f.path as NSString).lastPathComponent,
                size: f.size, modified: f.modified, checksum: f.checksum,
                namespace: f.namespace, chunks: f.chunks
            )
        }
        let dirs = (result.dirs ?? []).map { $0.path }
        return ListFilesResult(files: files, dirs: dirs)
    }

    struct PutParams: Codable {
        let path: String
        let localPath: String
        enum CodingKeys: String, CodingKey {
            case path
            case localPath = "local_path"
        }
    }

    struct GenericResult: Codable {
        let status: String?
        let size: Int64?
    }

    func putFile(path: String, localPath: String) async throws {
        let _: GenericResult = try await rpc.call("skyfs.put", params: PutParams(path: path, localPath: localPath))
    }

    struct GetParams: Codable {
        let path: String
        let outPath: String
        enum CodingKeys: String, CodingKey {
            case path
            case outPath = "out_path"
        }
    }

    func getFile(path: String, outPath: String) async throws {
        let _: GenericResult = try await rpc.call("skyfs.get", params: GetParams(path: path, outPath: outPath))
    }

    func removeFile(path: String) async throws {
        let _: GenericResult = try await rpc.call("skyfs.remove", params: ["path": path])
    }

    func getInfo() async throws -> StoreInfo {
        return try await rpc.call("skyfs.info")
    }

    // MARK: - Sync

    struct SyncStartParams: Codable {
        let dir: String
        let pollSeconds: Int
        enum CodingKeys: String, CodingKey {
            case dir
            case pollSeconds = "poll_seconds"
        }
    }

    func startSync(dir: String, pollSeconds: Int = 30) async throws {
        let _: GenericResult = try await rpc.call("skyfs.syncStart",
            params: SyncStartParams(dir: dir, pollSeconds: pollSeconds))
    }

    func stopSync() async throws {
        let _: GenericResult = try await rpc.call("skyfs.syncStop")
    }

    func syncStatus() async throws -> SyncStatusInfo {
        return try await rpc.call("skyfs.syncStatus")
    }

    // MARK: - Drives

    struct DriveCreateParams: Codable {
        let name: String
        let path: String
        let namespace: String
    }

    struct DriveIDParams: Codable {
        let id: String
    }

    struct DriveListResult: Codable {
        let drives: [DriveInfoResult]
    }

    struct DriveInfoResult: Codable {
        let id: String
        let name: String
        let localPath: String
        let namespace: String
        let enabled: Bool
        let running: Bool

        enum CodingKeys: String, CodingKey {
            case id, name, namespace, enabled, running
            case localPath = "local_path"
        }
    }

    func createDrive(name: String, path: String, namespace: String? = nil) async throws -> DriveInfoResult {
        return try await rpc.call("skyfs.driveCreate",
            params: DriveCreateParams(name: name, path: path, namespace: namespace ?? name))
    }

    func removeDrive(id: String) async throws {
        let _: GenericResult = try await rpc.call("skyfs.driveRemove", params: DriveIDParams(id: id))
    }

    func listDrives() async throws -> [DriveInfoResult] {
        let result: DriveListResult = try await rpc.call("skyfs.driveList")
        return result.drives
    }

    func startDrive(id: String) async throws {
        let _: GenericResult = try await rpc.call("skyfs.driveStart", params: DriveIDParams(id: id))
    }

    func stopDrive(id: String) async throws {
        let _: GenericResult = try await rpc.call("skyfs.driveStop", params: DriveIDParams(id: id))
    }

    // MARK: - Devices

    struct DeviceListResult: Codable {
        let devices: [DeviceInfo]?
        let thisDevice: String

        enum CodingKeys: String, CodingKey {
            case devices
            case thisDevice = "this_device"
        }
    }

    func listDevices() async throws -> DeviceListResponse {
        let result: DeviceListResult = try await rpc.call("skyfs.deviceList")
        return DeviceListResponse(devices: result.devices ?? [], thisDevice: result.thisDevice)
    }

    func removeDevice(pubkey: String) async throws {
        let _: GenericResult = try await rpc.call("skyfs.deviceRemove", params: ["pubkey": pubkey])
    }

    struct InviteResult: Codable {
        let code: String
    }

    func generateInvite() async throws -> String {
        let result: InviteResult = try await rpc.call("skyfs.invite")
        return result.code
    }

    struct JoinResult: Codable {
        let status: String
    }

    func joinInvite(inviteID: String) async throws -> String {
        let result: JoinResult = try await rpc.call("skyfs.join", params: ["invite_id": inviteID])
        return result.status
    }

    // MARK: - Sync Activity

    struct SyncActivityEntry: Codable, Identifiable {
        let direction: String  // "up" or "down"
        let op: String         // "put" or "delete"
        let path: String
        let driveID: String
        let driveName: String
        let ts: Int64

        var id: String { "\(direction)-\(path)-\(ts)" }

        enum CodingKeys: String, CodingKey {
            case direction, op, path, ts
            case driveID = "drive_id"
            case driveName = "drive_name"
        }
    }

    struct SyncActivityResult: Codable {
        let pending: [SyncActivityEntry]
    }

    func syncActivity() async throws -> [SyncActivityEntry] {
        let result: SyncActivityResult = try await rpc.call("skyfs.syncActivity")
        return result.pending
    }

    // MARK: - Health

    struct HealthResult: Codable {
        let status: String
        let version: String
        let uptime: String
        let drives: Int
        let drivesRunning: Int
        let outboxPending: Int
        let lastActivityAgo: String
        let rpcClients: Int
        let rpcSubscribers: Int
        let httpAddr: String?

        enum CodingKeys: String, CodingKey {
            case status, version, uptime, drives
            case drivesRunning = "drives_running"
            case outboxPending = "outbox_pending"
            case lastActivityAgo = "last_activity_ago"
            case rpcClients = "rpc_clients"
            case rpcSubscribers = "rpc_subscribers"
            case httpAddr = "http_addr"
        }
    }

    func health() async throws -> HealthResult {
        return try await rpc.call("skyfs.health")
    }

    // MARK: - Maintenance

    struct ResetResult: Codable {
        let s3Deleted: Int
        let localDeleted: Int

        enum CodingKeys: String, CodingKey {
            case s3Deleted = "s3_deleted"
            case localDeleted = "local_deleted"
        }
    }

    func reset() async throws -> ResetResult {
        return try await rpc.call("skyfs.reset")
    }

    struct CompactResult: Codable {
        let opsRemoved: Int
        let opsKept: Int
        let chunksRemoved: Int

        enum CodingKeys: String, CodingKey {
            case opsRemoved = "ops_removed"
            case opsKept = "ops_kept"
            case chunksRemoved = "chunks_removed"
        }
    }

    func compact(keep: Int = 3) async throws -> CompactResult {
        return try await rpc.call("skyfs.compact", params: ["keep": keep])
    }

    // MARK: - S3 Browser

    struct S3Entry: Codable, Identifiable {
        let key: String
        let size: Int64
        var id: String { key }
    }

    struct S3ListResult: Codable {
        let files: [S3Entry]?
        let dirs: [String]?
        let prefix: String
        let total: Int
    }

    func s3List(prefix: String) async throws -> S3ListResult {
        return try await rpc.call("skyfs.s3List", params: ["prefix": prefix])
    }

    struct S3DeleteResult: Codable {
        let deleted: String
    }

    func s3Delete(key: String) async throws -> S3DeleteResult {
        return try await rpc.call("skyfs.s3Delete", params: ["key": key])
    }

    // MARK: - Debug

    struct DebugDumpResult: Codable {
        let status: String
        let key: String
        let size: Int
    }

    func debugDump() async throws -> DebugDumpResult {
        return try await rpc.call("skyfs.debugDump")
    }

}
