import SwiftUI

// MARK: - Sample Data for Previews

extension FileNode {
    static let sampleFiles: [FileNode] = [
        FileNode(id: "journal/2026-03-14.md", path: "journal/2026-03-14.md",
                 name: "2026-03-14.md", size: 4523,
                 modified: "2026-03-14T09:15:00Z", checksum: "abcdef1234567890",
                 namespace: "journal", chunks: 1),
        FileNode(id: "journal/2026-03-13.md", path: "journal/2026-03-13.md",
                 name: "2026-03-13.md", size: 8901,
                 modified: "2026-03-13T22:00:00Z", checksum: "567890abcdef1234",
                 namespace: "journal", chunks: 1),
        FileNode(id: "financial/q4-report.pdf", path: "financial/q4-report.pdf",
                 name: "q4-report.pdf", size: 3_400_000,
                 modified: "2026-01-15T14:00:00Z", checksum: "fedcba9876543210",
                 namespace: "financial", chunks: 4),
        FileNode(id: "photos/sunset.jpg", path: "photos/sunset.jpg",
                 name: "sunset.jpg", size: 2_100_000,
                 modified: "2026-03-10T18:30:00Z", checksum: "1234567890abcdef",
                 namespace: "photos", chunks: 2),
        FileNode(id: "notes.md", path: "notes.md",
                 name: "notes.md", size: 256,
                 modified: "2026-03-14T12:00:00Z", checksum: "aabbccdd11223344",
                 namespace: "default", chunks: 1),
    ]
}

// Preview-only mock that lives in the main target (not test target)
private class PreviewSkyClient: SkyClientProtocol {
    func listFiles(prefix: String) async throws -> [FileNode] { FileNode.sampleFiles }
    func putFile(path: String, localPath: String) async throws {}
    func getFile(path: String, outPath: String) async throws {}
    func removeFile(path: String) async throws {}
    func getInfo() async throws -> StoreInfo {
        StoreInfo(id: "sky10qpreview", fileCount: 5, totalSize: 5_914_680,
                  namespaces: ["default", "financial", "journal", "photos"])
    }
    func startSync(dir: String, pollSeconds: Int) async throws {}
    func stopSync() async throws {}
    func syncStatus() async throws -> SyncStatusInfo {
        SyncStatusInfo(syncing: false, syncDir: nil)
    }
    func createDrive(name: String, path: String, namespace: String?) async throws -> SkyClient.DriveInfoResult {
        SkyClient.DriveInfoResult(id: "drive_\(name)", name: name, localPath: path, namespace: namespace ?? name, enabled: true, running: true)
    }
    func removeDrive(id: String) async throws {}
    func listDrives() async throws -> [SkyClient.DriveInfoResult] { [] }
    func startDrive(id: String) async throws {}
    func stopDrive(id: String) async throws {}
    func listDevices() async throws -> [DeviceInfo] { [] }
    func generateInvite() async throws -> String { "sky10invite_preview" }
}

extension AppState {
    static var preview: AppState {
        let state = AppState(client: PreviewSkyClient())
        state.files = FileNode.sampleFiles
        state.storeInfo = StoreInfo(
            id: "sky10qpreview1234567890", fileCount: 5, totalSize: 5_914_680,
            namespaces: ["default", "financial", "journal", "photos"]
        )
        state.syncState = .synced
        return state
    }

    static var previewError: AppState {
        let state = AppState(client: PreviewSkyClient())
        state.syncState = .error
        state.error = "Connection to S3 failed"
        return state
    }

    static var previewEmpty: AppState {
        let state = AppState(client: PreviewSkyClient())
        state.syncState = .synced
        state.storeInfo = StoreInfo(id: "sky10qempty", fileCount: 0, totalSize: 0, namespaces: nil)
        return state
    }
}

// #Preview macros require Xcode — they don't compile under SPM.
// When using Xcode, add previews in a separate file or enable here.
