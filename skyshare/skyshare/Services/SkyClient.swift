import Foundation

/// High-level client for skyfs backend operations.
class SkyClient {
    private let rpc = RPCClient()

    struct ListResult: Codable {
        let files: [FileResult]
    }

    struct FileResult: Codable {
        let path: String
        let size: Int64
        let modified: String
        let checksum: String
        let namespace: String
        let chunks: Int
    }

    func listFiles(prefix: String) async throws -> [FileNode] {
        let result: ListResult = try await rpc.call("skyfs.list", params: ["prefix": prefix])
        return result.files.map { f in
            FileNode(
                id: f.path, path: f.path,
                name: (f.path as NSString).lastPathComponent,
                size: f.size, modified: f.modified, checksum: f.checksum,
                namespace: f.namespace, chunks: f.chunks
            )
        }
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
}
