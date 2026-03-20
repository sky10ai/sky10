import Foundation
import Testing
@testable import cirrus

@Suite("AppState")
@MainActor
struct AppStateTests {

    @Test("Initial state")
    func initialState() {
        let state = AppState(client: MockSkyClient())
        #expect(state.syncState == .offline)
        #expect(state.files.isEmpty)
        #expect(state.storeInfo == nil)
        #expect(state.isLoading == false)
        #expect(state.error == nil)
    }

    @Test("Refresh loads info and files from drive directories")
    func refreshSuccess() async throws {
        let mock = MockSkyClient()
        mock.info = StoreInfo(id: "sky10qtest", fileCount: 2, totalSize: 300, namespaces: ["default"])

        let tmpDir = FileManager.default.temporaryDirectory
            .appendingPathComponent("cirrus-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tmpDir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: tmpDir) }

        try "hello".write(to: tmpDir.appendingPathComponent("a.md"), atomically: true, encoding: .utf8)
        try "world".write(to: tmpDir.appendingPathComponent("b.md"), atomically: true, encoding: .utf8)

        mock.mockDrives = [
            SkyClient.DriveInfoResult(id: "drive_Test", name: "Test",
                localPath: tmpDir.path, namespace: "default", enabled: true, running: true)
        ]

        let state = AppState(client: mock)
        await state.loadDrives()
        await state.refresh()

        #expect(state.syncState == .synced)
        #expect(state.files.count == 2)
        #expect(state.storeInfo?.fileCount == 2)
        #expect(state.error == nil)
        #expect(state.isLoading == false)
    }

    @Test("Refresh error sets error state")
    func refreshError() async {
        let mock = MockSkyClient()
        mock.shouldError = true
        mock.errorMessage = "connection refused"

        let state = AppState(client: mock)
        await state.refresh()

        #expect(state.syncState == .error)
        #expect(state.error != nil)
        #expect(state.error!.contains("connection refused"))
    }

    @Test("Upload file calls put")
    func upload() async {
        let mock = MockSkyClient()
        let state = AppState(client: mock)
        await state.uploadFile(localPath: "/tmp/test.md", remotePath: "docs/test.md")

        #expect(mock.putCalls.count == 1)
        #expect(mock.putCalls[0].path == "docs/test.md")
        #expect(mock.putCalls[0].localPath == "/tmp/test.md")
    }

    @Test("Upload error sets error state")
    func uploadError() async {
        let mock = MockSkyClient()
        mock.shouldError = true

        let state = AppState(client: mock)
        await state.uploadFile(localPath: "/tmp/test.md", remotePath: "test.md")

        #expect(state.syncState == .error)
        #expect(state.error != nil)
    }

    @Test("Download file calls get")
    func download() async {
        let mock = MockSkyClient()
        let state = AppState(client: mock)
        await state.downloadFile(remotePath: "docs/readme.md", localPath: "/tmp/readme.md")

        #expect(mock.getCalls.count == 1)
        #expect(mock.getCalls[0].path == "docs/readme.md")
        #expect(state.syncState == .synced)
    }

    @Test("Download error sets error state")
    func downloadError() async {
        let mock = MockSkyClient()
        mock.shouldError = true

        let state = AppState(client: mock)
        await state.downloadFile(remotePath: "file.md", localPath: "/tmp/file.md")

        #expect(state.syncState == .error)
        #expect(state.error != nil)
    }

    @Test("Remove file calls remove")
    func removeFile() async throws {
        let mock = MockSkyClient()
        let state = AppState(client: mock)

        await state.removeFile(path: "rm.md")
        #expect(mock.removeCalls == ["rm.md"])
    }

    @Test("Namespaces computed from drive files")
    func namespacesComputed() async throws {
        let mock = MockSkyClient()

        let tmpDir = FileManager.default.temporaryDirectory
            .appendingPathComponent("cirrus-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tmpDir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: tmpDir) }

        try "a".write(to: tmpDir.appendingPathComponent("a.md"), atomically: true, encoding: .utf8)

        let tmpDir2 = FileManager.default.temporaryDirectory
            .appendingPathComponent("cirrus-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: tmpDir2, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: tmpDir2) }

        try "b".write(to: tmpDir2.appendingPathComponent("b.md"), atomically: true, encoding: .utf8)

        mock.mockDrives = [
            SkyClient.DriveInfoResult(id: "drive_default", name: "Default",
                localPath: tmpDir.path, namespace: "default", enabled: true, running: true),
            SkyClient.DriveInfoResult(id: "drive_journal", name: "Journal",
                localPath: tmpDir2.path, namespace: "journal", enabled: true, running: true),
        ]

        let state = AppState(client: mock)
        await state.loadDrives()
        await state.refresh()

        #expect(state.namespaces == ["default", "journal"])
    }
}
