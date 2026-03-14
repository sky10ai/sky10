import Foundation

/// Protocol for skyfs backend operations. Enables mocking in tests.
protocol SkyClientProtocol {
    func listFiles(prefix: String) async throws -> [FileNode]
    func putFile(path: String, localPath: String) async throws
    func getFile(path: String, outPath: String) async throws
    func removeFile(path: String) async throws
    func getInfo() async throws -> StoreInfo
}

// Conform the real client
extension SkyClient: SkyClientProtocol {}
