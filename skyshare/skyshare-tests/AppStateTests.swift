import XCTest
@testable import skyshare

@MainActor
final class AppStateTests: XCTestCase {

    func testInitialState() {
        let state = AppState(client: MockSkyClient())
        XCTAssertEqual(state.syncState, .offline)
        XCTAssertTrue(state.files.isEmpty)
        XCTAssertNil(state.storeInfo)
        XCTAssertNil(state.selectedNamespace)
        XCTAssertFalse(state.isLoading)
        XCTAssertNil(state.error)
    }

    func testRefreshSuccess() async {
        let mock = MockSkyClient()
        mock.files = [
            FileNode(id: "a.md", path: "a.md", name: "a.md", size: 100,
                     modified: "2026-03-14T10:00:00Z", checksum: "h1",
                     namespace: "default", chunks: 1),
            FileNode(id: "journal/b.md", path: "journal/b.md", name: "b.md",
                     size: 200, modified: "2026-03-14T11:00:00Z", checksum: "h2",
                     namespace: "journal", chunks: 1),
        ]
        mock.info = StoreInfo(id: "sky://k1_test", fileCount: 2, totalSize: 300, namespaces: ["default", "journal"])

        let state = AppState(client: mock)
        await state.refresh()

        XCTAssertEqual(state.syncState, .synced)
        XCTAssertEqual(state.files.count, 2)
        XCTAssertEqual(state.storeInfo?.fileCount, 2)
        XCTAssertEqual(state.storeInfo?.totalSize, 300)
        XCTAssertNil(state.error)
        XCTAssertFalse(state.isLoading)
    }

    func testRefreshError() async {
        let mock = MockSkyClient()
        mock.shouldError = true
        mock.errorMessage = "connection refused"

        let state = AppState(client: mock)
        await state.refresh()

        XCTAssertEqual(state.syncState, .error)
        XCTAssertNotNil(state.error)
        XCTAssertTrue(state.error!.contains("connection refused"))
    }

    func testUploadFile() async {
        let mock = MockSkyClient()
        let state = AppState(client: mock)

        await state.uploadFile(localPath: "/tmp/test.md", remotePath: "docs/test.md")

        XCTAssertEqual(mock.putCalls.count, 1)
        XCTAssertEqual(mock.putCalls[0].path, "docs/test.md")
        XCTAssertEqual(mock.putCalls[0].localPath, "/tmp/test.md")
    }

    func testUploadError() async {
        let mock = MockSkyClient()
        mock.shouldError = true

        let state = AppState(client: mock)
        await state.uploadFile(localPath: "/tmp/test.md", remotePath: "test.md")

        XCTAssertEqual(state.syncState, .error)
        XCTAssertNotNil(state.error)
    }

    func testDownloadFile() async {
        let mock = MockSkyClient()
        let state = AppState(client: mock)

        await state.downloadFile(remotePath: "docs/readme.md", localPath: "/tmp/readme.md")

        XCTAssertEqual(mock.getCalls.count, 1)
        XCTAssertEqual(mock.getCalls[0].path, "docs/readme.md")
        XCTAssertEqual(state.syncState, .synced)
    }

    func testDownloadError() async {
        let mock = MockSkyClient()
        mock.shouldError = true

        let state = AppState(client: mock)
        await state.downloadFile(remotePath: "file.md", localPath: "/tmp/file.md")

        XCTAssertEqual(state.syncState, .error)
        XCTAssertNotNil(state.error)
    }

    func testRemoveFile() async {
        let mock = MockSkyClient()
        mock.files = [
            FileNode(id: "rm.md", path: "rm.md", name: "rm.md", size: 50,
                     modified: "2026-03-14T10:00:00Z", checksum: "h1",
                     namespace: "default", chunks: 1)
        ]
        mock.info = StoreInfo(id: "sky://k1_test", fileCount: 1, totalSize: 50, namespaces: ["default"])

        let state = AppState(client: mock)
        await state.refresh()
        XCTAssertEqual(state.files.count, 1)

        await state.removeFile(path: "rm.md")

        XCTAssertEqual(mock.removeCalls, ["rm.md"])
        XCTAssertEqual(state.files.count, 0)
    }

    func testFilteredFiles() async {
        let mock = MockSkyClient()
        mock.files = [
            FileNode(id: "a.md", path: "a.md", name: "a.md", size: 10,
                     modified: "", checksum: "", namespace: "default", chunks: 1),
            FileNode(id: "journal/b.md", path: "journal/b.md", name: "b.md",
                     size: 20, modified: "", checksum: "", namespace: "journal", chunks: 1),
            FileNode(id: "journal/c.md", path: "journal/c.md", name: "c.md",
                     size: 30, modified: "", checksum: "", namespace: "journal", chunks: 1),
        ]

        let state = AppState(client: mock)
        await state.refresh()

        // No filter — all files
        XCTAssertEqual(state.filteredFiles.count, 3)

        // Filter by journal
        state.selectedNamespace = "journal"
        XCTAssertEqual(state.filteredFiles.count, 2)
        XCTAssertTrue(state.filteredFiles.allSatisfy { $0.namespace == "journal" })

        // Filter by default
        state.selectedNamespace = "default"
        XCTAssertEqual(state.filteredFiles.count, 1)

        // Clear filter
        state.selectedNamespace = nil
        XCTAssertEqual(state.filteredFiles.count, 3)
    }

    func testNamespaces() async {
        let mock = MockSkyClient()
        mock.files = [
            FileNode(id: "1", path: "a.md", name: "a.md", size: 0, modified: "",
                     checksum: "", namespace: "default", chunks: 1),
            FileNode(id: "2", path: "b.md", name: "b.md", size: 0, modified: "",
                     checksum: "", namespace: "journal", chunks: 1),
            FileNode(id: "3", path: "c.md", name: "c.md", size: 0, modified: "",
                     checksum: "", namespace: "journal", chunks: 1),
            FileNode(id: "4", path: "d.md", name: "d.md", size: 0, modified: "",
                     checksum: "", namespace: "financial", chunks: 1),
        ]

        let state = AppState(client: mock)
        await state.refresh()

        XCTAssertEqual(state.namespaces, ["default", "financial", "journal"])
    }
}
