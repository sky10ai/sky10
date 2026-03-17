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

    @Test("Refresh loads files and info")
    func refreshSuccess() async {
        let mock = MockSkyClient()
        mock.files = [
            FileNode(id: "a.md", path: "a.md", name: "a.md", size: 100,
                     modified: "2026-03-14T10:00:00Z", checksum: "h1",
                     namespace: "default", chunks: 1),
            FileNode(id: "journal/b.md", path: "journal/b.md", name: "b.md",
                     size: 200, modified: "2026-03-14T11:00:00Z", checksum: "h2",
                     namespace: "journal", chunks: 1),
        ]
        mock.info = StoreInfo(id: "sky10qtest", fileCount: 2, totalSize: 300, namespaces: ["default", "journal"])

        let state = AppState(client: mock)
        await state.refresh()

        #expect(state.syncState == .synced)
        #expect(state.files.count == 2)
        #expect(state.storeInfo?.fileCount == 2)
        #expect(state.storeInfo?.totalSize == 300)
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

    @Test("Remove file calls remove and refreshes")
    func removeFile() async {
        let mock = MockSkyClient()
        mock.files = [
            FileNode(id: "rm.md", path: "rm.md", name: "rm.md", size: 50,
                     modified: "2026-03-14T10:00:00Z", checksum: "h1",
                     namespace: "default", chunks: 1)
        ]
        mock.info = StoreInfo(id: "sky10qtest", fileCount: 1, totalSize: 50, namespaces: ["default"])

        let state = AppState(client: mock)
        await state.refresh()
        #expect(state.files.count == 1)

        await state.removeFile(path: "rm.md")
        #expect(mock.removeCalls == ["rm.md"])
        #expect(state.files.count == 0)
    }

    @Test("Namespaces computed and sorted")
    func namespacesComputed() async {
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

        #expect(state.namespaces == ["default", "journal"])
    }

    @Test("Namespaces computed and sorted")
    func namespaces() async {
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

        #expect(state.namespaces == ["default", "financial", "journal"])
    }
}
