import Foundation

/// High-level client for skyfs backend operations.
class SkyClient {
    private let rpc = RPCClient()

    // MARK: - File Operations

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
                id: f.path,
                path: f.path,
                name: (f.path as NSString).lastPathComponent,
                size: f.size,
                modified: f.modified,
                checksum: f.checksum,
                namespace: f.namespace,
                chunks: f.chunks
            )
        }
    }

    struct PutParams: Codable {
        let path: String
        let local_path: String
    }

    func putFile(path: String, localPath: String) async throws {
        let _: [String: Any] = try await rpc.call("skyfs.put", params: PutParams(path: path, local_path: localPath))
    }

    struct GetParams: Codable {
        let path: String
        let out_path: String
    }

    func getFile(path: String, outPath: String) async throws {
        let _: [String: Any] = try await rpc.call("skyfs.get", params: GetParams(path: path, out_path: outPath))
    }

    func removeFile(path: String) async throws {
        let _: [String: Any] = try await rpc.call("skyfs.remove", params: ["path": path])
    }

    func getInfo() async throws -> StoreInfo {
        return try await rpc.call("skyfs.info")
    }
}

// Make dictionary decodable for generic responses
extension Dictionary: @retroactive Decodable where Key == String, Value == Any {
    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        let data = try container.decode(Data.self)
        let json = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        self = json ?? [:]
    }
}
