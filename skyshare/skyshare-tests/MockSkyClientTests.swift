import Testing
@testable import skyshare

@Suite("MockSkyClient")
struct MockSkyClientTests {

    @Test("List files empty")
    func listEmpty() async throws {
        let mock = MockSkyClient()
        let files = try await mock.listFiles(prefix: "")
        #expect(files.isEmpty)
    }

    @Test("List files with prefix filter")
    func listWithPrefix() async throws {
        let mock = MockSkyClient()
        mock.files = [
            FileNode(id: "docs/a.md", path: "docs/a.md", name: "a.md", size: 10,
                     modified: "", checksum: "", namespace: "docs", chunks: 1),
            FileNode(id: "other/b.md", path: "other/b.md", name: "b.md", size: 20,
                     modified: "", checksum: "", namespace: "other", chunks: 1),
        ]
        let docsOnly = try await mock.listFiles(prefix: "docs/")
        #expect(docsOnly.count == 1)
        #expect(docsOnly[0].path == "docs/a.md")
    }

    @Test("Put adds file and updates info")
    func put() async throws {
        let mock = MockSkyClient()
        try await mock.putFile(path: "new.md", localPath: "/tmp/new.md")
        #expect(mock.files.count == 1)
        #expect(mock.files[0].path == "new.md")
        #expect(mock.info.fileCount == 1)
    }

    @Test("Remove deletes file")
    func remove() async throws {
        let mock = MockSkyClient()
        try await mock.putFile(path: "rm.md", localPath: "/tmp/rm.md")
        #expect(mock.files.count == 1)
        try await mock.removeFile(path: "rm.md")
        #expect(mock.files.isEmpty)
        #expect(mock.info.fileCount == 0)
    }

    @Test("Error mode throws")
    func errorMode() async {
        let mock = MockSkyClient()
        mock.shouldError = true
        mock.errorMessage = "test error"

        await #expect(throws: MockError.self) {
            _ = try await mock.listFiles(prefix: "")
        }
    }

    @Test("Get records calls")
    func getRecords() async throws {
        let mock = MockSkyClient()
        try await mock.getFile(path: "file.md", outPath: "/tmp/out.md")
        #expect(mock.getCalls.count == 1)
        #expect(mock.getCalls[0].path == "file.md")
        #expect(mock.getCalls[0].outPath == "/tmp/out.md")
    }
}
